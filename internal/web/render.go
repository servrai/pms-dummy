package web

import (
	"embed"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

//go:embed templates/*.html static/*.css
var content embed.FS

type templateT = template.Template

var templatePages = []string{
	"login", "dashboard", "rooms", "reservations", "reservation_view",
	"settings", "secret", "actions_log", "events_log", "outbound",
}

func parseTemplates() map[string]*templateT {
	out := map[string]*templateT{}
	for _, page := range templatePages {
		t := template.Must(template.ParseFS(content, "templates/layout.html", "templates/"+page+".html"))
		out[page] = t
	}
	return out
}

// render executes layout+page. Pages other than login hide nothing special;
// the layout nav is always rendered.
func (s *Server) render(w http.ResponseWriter, page string, data any) {
	t, ok := s.tpl[page]
	if !ok {
		http.Error(w, "template missing: "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "render error: "+err.Error(), http.StatusInternalServerError)
	}
}

// seeOther is the PRG redirect used by every mutating form handler.
func seeOther(w http.ResponseWriter, r *http.Request, path string, msg string) {
	if msg != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + "msg=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}
