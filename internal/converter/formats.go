package converter

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

var supportedDocumentExtensions = map[string]struct{}{
	".pdf":  {},
	".ppt":  {},
	".pptx": {},
	".doc":  {},
	".docx": {},
	".odt":  {},
	".odp":  {},
	".rtf":  {},
}

// SupportedExtensions returns the accepted input formats in stable order.
func SupportedExtensions() []string {
	extensions := make([]string, 0, len(supportedDocumentExtensions))
	for extension := range supportedDocumentExtensions {
		extensions = append(extensions, extension)
	}
	sort.Strings(extensions)
	return extensions
}

// ValidateSourceFormat rejects extensions that LibreOffice conversion does
// not explicitly support.
func ValidateSourceFormat(path string) error {
	extension := strings.ToLower(filepath.Ext(path))
	if _, ok := supportedDocumentExtensions[extension]; !ok {
		return newConversionError(ErrUnsupportedFormat, "inspect source", fmt.Errorf("%q; supported formats: %s", extension, strings.Join(SupportedExtensions(), ", ")))
	}
	return nil
}
