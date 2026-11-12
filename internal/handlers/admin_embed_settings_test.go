package handlers

import (
	"bytes"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/metavert-io/flipbook/internal/models"
)

func adminSettingsRequest(overrides url.Values) *http.Request {
	form := url.Values{
		"title": {"Report"}, "language": {"is"}, "visibility": {"public"},
		"embed_enabled": {"on"}, "embed_start_page": {"1"}, "sales_currency": {"ISK"},
	}
	for key, values := range overrides {
		form[key] = values
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/flipbooks/fb/settings", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestApplyAdminSettingsSavesEmbedSize(t *testing.T) {
	fb := &models.Flipbook{ID: "fb", PageCount: 10}
	if err := applyAdminSettings(fb, adminSettingsRequest(url.Values{"embed_width": {"1600"}, "embed_height": {"900"}})); err != nil {
		t.Fatal(err)
	}
	if fb.Embed.Width != 1600 || fb.Embed.Height != 900 {
		t.Fatalf("saved embed size = %dx%d, want 1600x900", fb.Embed.Width, fb.Embed.Height)
	}

	if err := applyAdminSettings(fb, adminSettingsRequest(url.Values{"embed_width": {""}, "embed_height": {""}})); err != nil {
		t.Fatal(err)
	}
	if fb.Embed.Width != 0 || fb.Embed.Height != 0 || fb.Embed.FrameWidth() != 1200 || fb.Embed.FrameHeight() != 800 {
		t.Fatalf("blank size should return to the 1200x800 default, got %+v", fb.Embed)
	}

	for _, size := range [][2]string{{"199", "800"}, {"1200", "4001"}, {"wide", "800"}} {
		err := applyAdminSettings(fb, adminSettingsRequest(url.Values{"embed_width": {size[0]}, "embed_height": {size[1]}}))
		if err == nil {
			t.Errorf("embed size %sx%s was accepted", size[0], size[1])
		}
	}
}

func TestAdminEmbedCodeUsesSavedSize(t *testing.T) {
	code := embedCode("https://reader.example", "report", models.EmbedSettings{})
	if !strings.Contains(code, `width="1200" height="800"`) {
		t.Fatalf("default embed code = %s", code)
	}
	code = embedCode("https://reader.example", "report", models.EmbedSettings{Width: 960, Height: 540})
	if !strings.Contains(code, `width="960" height="540"`) {
		t.Fatalf("saved embed code = %s", code)
	}
}

func renderAdminDetail(t *testing.T, fb *models.Flipbook) string {
	t.Helper()
	path := "../../web/templates/admin/detail.html"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		path = "/src/web/templates/admin/detail.html"
	}
	stubs := `{{define "admin_head"}}{{end}}{{define "admin_nav"}}{{end}}{{define "admin_flash"}}{{end}}{{define "admin_csrf"}}{{end}}{{define "admin_scripts"}}{{end}}`
	tmpl := template.Must(template.Must(template.New("").Parse(stubs)).ParseFiles(path))
	var output bytes.Buffer
	data := map[string]any{"Flipbook": fb, "CanEdit": true, "CanPublish": true, "BaseURL": "https://reader.example"}
	if err := tmpl.ExecuteTemplate(&output, "admin_detail", data); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func TestAdminDetailEmbedLayout(t *testing.T) {
	fb := &models.Flipbook{ID: "fb", Slug: "report", Title: "Report", Status: models.StatusReady, Embed: models.DefaultEmbedSettings()}
	page := renderAdminDetail(t, fb)

	configurator := strings.Index(page, `id="embed-config-heading"`)
	behavior := strings.Index(page, `<summary>Embed behavior</summary>`)
	settings := strings.Index(page, `id="publication-settings"`)
	if configurator < 0 || behavior < 0 || settings < 0 || !(configurator < behavior && behavior < settings) {
		t.Fatalf("embed behavior must sit between the configurator and the settings form (configurator=%d behavior=%d settings=%d)", configurator, behavior, settings)
	}
	for _, expected := range []string{
		`name="embed_width" form="publication-settings" value="1200"`,
		`name="embed_height" form="publication-settings" value="800"`,
		`name="embed_toolbar" form="publication-settings" >`,
		`name="embed_search" form="publication-settings" >`,
		`name="embed_background" form="publication-settings" value="#FFFFFF"`,
		`<input type="checkbox" name="hide_branding" > Hide Penninn Midlun attribution`,
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("detail page is missing %q", expected)
		}
	}
	behaviorBlock := page[behavior:settings]
	for _, name := range []string{"embed_enabled", "embed_toolbar", "embed_search", "embed_share", "embed_download", "embed_domains", "embed_start_page", "embed_reading_mode", "embed_background"} {
		if !strings.Contains(behaviorBlock, `name="`+name+`" form="publication-settings"`) {
			t.Errorf("%s is outside the settings form and not bound to it with form=\"publication-settings\"", name)
		}
	}

	fb.Embed.Width, fb.Embed.Height = 1600, 900
	page = renderAdminDetail(t, fb)
	if !strings.Contains(page, `name="embed_width" form="publication-settings" value="1600"`) || !strings.Contains(page, `name="embed_height" form="publication-settings" value="900"`) {
		t.Fatal("configurator does not reload the saved embed size")
	}
}
