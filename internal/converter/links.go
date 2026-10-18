package converter

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	pageFilePattern         = regexp.MustCompile(`(?i)(?:^|/)[^/]*-([0-9]+)\.html?(?:#.*)?$`)
	pageHashPattern         = regexp.MustCompile(`(?i)^#(?:page=)?([0-9]+)$`)
	pageDocumentHashPattern = regexp.MustCompile(`(?i)(?:^|/)[^/]+\.html#([0-9]+)$`)
)

type xmlNode struct {
	name     xml.Name
	attrs    []xml.Attr
	text     string
	children []*xmlNode
	parent   *xmlNode
}

func (c *Converter) extractLinksAndOutline(ctx context.Context, pdfPath string) (map[int][]Link, []OutlineItem, []Warning) {
	links := make(map[int][]Link)
	result, err := c.runCommand(ctx, c.options.Tools.PDFToHTML,
		"-xml", "-stdout", "-i", "-hidden", "-noframes", "-noroundcoord", "-zoom", "1.0", pdfPath)
	if err != nil {
		return links, nil, []Warning{{Code: "link_extraction_unavailable", Message: commandError(c.options.Tools.PDFToHTML, err, result).Error()}}
	}
	warnings := warningsFromCommand("pdftohtml", result.stderr, 0)
	root, err := parseXMLTree(result.stdout)
	if err != nil {
		warnings = append(warnings, Warning{Code: "link_extraction_unavailable", Message: err.Error()})
		return links, nil, warnings
	}
	destinations, destinationWarnings := c.namedDestinations(ctx, pdfPath)
	warnings = append(warnings, destinationWarnings...)
	for _, page := range descendants(root, "page") {
		pageNumber, err := strconv.Atoi(page.attr("number"))
		if err != nil || pageNumber < 1 {
			continue
		}
		for _, anchor := range descendants(page, "a") {
			href := strings.TrimSpace(anchor.attr("href"))
			link, ok := classifyLink(href, destinations)
			if !ok {
				continue
			}
			rect, ok := anchorRect(anchor)
			if !ok {
				continue
			}
			link.Rect = rect
			links[pageNumber] = append(links[pageNumber], link)
		}
	}
	outline := parseOutline(root, destinations)
	return links, outline, warnings
}

func (c *Converter) namedDestinations(ctx context.Context, pdfPath string) (map[string]int, []Warning) {
	result, err := c.runCommand(ctx, c.options.Tools.PDFInfo, "-dests", pdfPath)
	if err != nil {
		return nil, []Warning{{Code: "named_destinations_unavailable", Message: commandError(c.options.Tools.PDFInfo, err, result).Error()}}
	}
	destinations := make(map[string]int)
	for _, line := range strings.Split(string(result.stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		page, err := strconv.Atoi(fields[0])
		if err != nil || page < 1 {
			continue
		}
		start := strings.Index(line, "\"")
		end := strings.LastIndex(line, "\"")
		if start < 0 || end <= start {
			continue
		}
		name := line[start+1 : end]
		if name != "" {
			destinations[name] = page
		}
	}
	return destinations, nil
}

func parseXMLTree(data []byte) (*xmlNode, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	root := &xmlNode{name: xml.Name{Local: "document"}}
	stack := []*xmlNode{root}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse pdftohtml XML: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			parent := stack[len(stack)-1]
			node := &xmlNode{name: value.Name, attrs: value.Attr, parent: parent}
			parent.children = append(parent.children, node)
			stack = append(stack, node)
		case xml.CharData:
			stack[len(stack)-1].text += string(value)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return root, nil
}

func descendants(node *xmlNode, name string) []*xmlNode {
	var matches []*xmlNode
	var walk func(*xmlNode)
	walk = func(current *xmlNode) {
		for _, child := range current.children {
			if child.name.Local == name {
				matches = append(matches, child)
			}
			walk(child)
		}
	}
	walk(node)
	return matches
}

func (n *xmlNode) attr(name string) string {
	for _, attr := range n.attrs {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}

func classifyLink(href string, destinations map[string]int) (Link, bool) {
	if href == "" {
		return Link{}, false
	}
	for _, pattern := range []*regexp.Regexp{pageHashPattern, pageFilePattern, pageDocumentHashPattern} {
		if match := pattern.FindStringSubmatch(href); len(match) == 2 {
			page, _ := strconv.Atoi(match[1])
			return Link{Kind: "internal", TargetPage: page}, page > 0
		}
	}
	if strings.HasPrefix(href, "#") {
		name := strings.TrimPrefix(href, "#")
		if name == "" {
			return Link{}, false
		}
		return Link{Kind: "internal", Destination: name, TargetPage: destinations[name]}, true
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return Link{}, false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto", "tel":
		return Link{Kind: "external", URI: href}, true
	default:
		return Link{}, false
	}
}

func anchorRect(anchor *xmlNode) (Rect, bool) {
	var result Rect
	found := false
	if rect, ok := rectFromXMLNode(anchor); ok {
		result = rect
		found = true
	}
	for _, node := range descendants(anchor, "text") {
		rect, ok := rectFromXMLNode(node)
		if !ok {
			continue
		}
		if !found {
			result = rect
			found = true
			continue
		}
		minX := minFloat(result.X, rect.X)
		minY := minFloat(result.Y, rect.Y)
		maxX := maxFloat(result.X+result.Width, rect.X+rect.Width)
		maxY := maxFloat(result.Y+result.Height, rect.Y+rect.Height)
		result = Rect{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY}
	}
	if !found && anchor.parent != nil && anchor.parent.name.Local == "text" {
		result, found = rectFromXMLNode(anchor.parent)
	}
	return result, found && result.Width > 0 && result.Height > 0
}

func rectFromXMLNode(node *xmlNode) (Rect, bool) {
	left, errLeft := strconv.ParseFloat(node.attr("left"), 64)
	top, errTop := strconv.ParseFloat(node.attr("top"), 64)
	width, errWidth := strconv.ParseFloat(node.attr("width"), 64)
	height, errHeight := strconv.ParseFloat(node.attr("height"), 64)
	if errLeft != nil || errTop != nil || errWidth != nil || errHeight != nil {
		return Rect{}, false
	}
	return Rect{X: left, Y: top, Width: width, Height: height}, true
}

func parseOutline(root *xmlNode, destinations map[string]int) []OutlineItem {
	outlines := descendants(root, "outline")
	if len(outlines) == 0 {
		return nil
	}
	var items []OutlineItem
	for _, outline := range outlines {
		for _, child := range outline.children {
			if child.name.Local == "item" {
				items = append(items, outlineItem(child, destinations))
			}
		}
	}
	return items
}

func outlineItem(node *xmlNode, destinations map[string]int) OutlineItem {
	title := strings.TrimSpace(node.attr("title"))
	if title == "" {
		title = strings.TrimSpace(node.text)
	}
	destination := strings.TrimSpace(node.attr("dest"))
	if destination == "" {
		destination = strings.TrimSpace(node.attr("href"))
	}
	page, _ := strconv.Atoi(node.attr("page"))
	if page == 0 && destination != "" {
		page = destinations[strings.TrimPrefix(destination, "#")]
	}
	item := OutlineItem{Title: title, TargetPage: page, Destination: destination}
	for _, child := range node.children {
		if child.name.Local == "item" {
			item.Children = append(item.Children, outlineItem(child, destinations))
		}
	}
	return item
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
