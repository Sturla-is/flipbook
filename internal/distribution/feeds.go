package distribution

import (
	"encoding/xml"
	"strings"
	"time"
)

type SitemapEntry struct {
	Location   string
	ModifiedAt time.Time
	ChangeFreq string
	Priority   string
}

type sitemapDocument struct {
	XMLName xml.Name       `xml:"urlset"`
	Xmlns   string         `xml:"xmlns,attr"`
	Entries []sitemapEntry `xml:"url"`
}

type sitemapEntry struct {
	Location   string `xml:"loc"`
	ModifiedAt string `xml:"lastmod,omitempty"`
	ChangeFreq string `xml:"changefreq,omitempty"`
	Priority   string `xml:"priority,omitempty"`
}

func Sitemap(entries []SitemapEntry) ([]byte, error) {
	document := sitemapDocument{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9", Entries: make([]sitemapEntry, 0, len(entries))}
	for _, entry := range entries {
		if SafeExternalURL(entry.Location) == "" {
			continue
		}
		item := sitemapEntry{Location: entry.Location, ChangeFreq: entry.ChangeFreq, Priority: entry.Priority}
		if !entry.ModifiedAt.IsZero() {
			item.ModifiedAt = entry.ModifiedAt.UTC().Format("2006-01-02")
		}
		document.Entries = append(document.Entries, item)
	}
	body, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}

type FeedItem struct {
	Title       string
	Link        string
	Description string
	GUID        string
	PublishedAt time.Time
}

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Generator   string    `xml:"generator"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string  `xml:"title"`
	Link        string  `xml:"link"`
	Description string  `xml:"description"`
	GUID        rssGUID `xml:"guid"`
	PublishedAt string  `xml:"pubDate,omitempty"`
}

type rssGUID struct {
	IsPermaLink bool   `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

func RSS(title, link, description string, items []FeedItem) ([]byte, error) {
	document := rssDocument{Version: "2.0", Channel: rssChannel{
		Title: title, Link: link, Description: description, Generator: "Flipbook",
		Items: make([]rssItem, 0, len(items)),
	}}
	for _, item := range items {
		if SafeExternalURL(item.Link) == "" {
			continue
		}
		guid := strings.TrimSpace(item.GUID)
		if guid == "" {
			guid = item.Link
		}
		entry := rssItem{Title: item.Title, Link: item.Link, Description: item.Description, GUID: rssGUID{IsPermaLink: SafeExternalURL(guid) != "", Value: guid}}
		if !item.PublishedAt.IsZero() {
			entry.PublishedAt = item.PublishedAt.UTC().Format(time.RFC1123Z)
		}
		document.Channel.Items = append(document.Channel.Items, entry)
	}
	body, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}

func Robots(baseURL string) string {
	lines := []string{
		"User-agent: *",
		"Allow: /",
		"Disallow: /admin",
		"Disallow: /api/",
		"Disallow: /embed/",
		"Disallow: /data/",
	}
	if sitemap := AbsoluteURL(baseURL, "/sitemap.xml"); SafeExternalURL(sitemap) != "" {
		lines = append(lines, "Sitemap: "+sitemap)
	}
	return strings.Join(lines, "\n") + "\n"
}
