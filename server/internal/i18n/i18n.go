// Package i18n translates the web UI. English is the default and the
// fallback for missing keys; locales live in locales/<code>.json.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

//go:embed locales/*.json
var files embed.FS

type Language struct {
	Code string
	Name string
}

// Languages offered in the language picker, in display order.
var Languages = []Language{
	{"en", "English"},
	{"cs", "Čeština"},
	{"sk", "Slovenčina"},
	{"de", "Deutsch"},
	{"fr", "Français"},
	{"es", "Español"},
	{"it", "Italiano"},
	{"pl", "Polski"},
}

var dicts = map[string]map[string]string{}

func init() {
	for _, l := range Languages {
		b, err := files.ReadFile("locales/" + l.Code + ".json")
		if err != nil {
			panic(err)
		}
		d := map[string]string{}
		if err := json.Unmarshal(b, &d); err != nil {
			panic(fmt.Sprintf("locale %s: %v", l.Code, err))
		}
		dicts[l.Code] = d
	}
}

// Supported reports whether code is one of Languages.
func Supported(code string) bool {
	_, ok := dicts[code]
	return ok
}

// T translates key into lang; args are applied with fmt.Sprintf.
func T(lang, key string, args ...any) string {
	s, ok := dicts[lang][key]
	if !ok {
		if s, ok = dicts["en"][key]; !ok {
			s = key
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// Has reports whether key exists (in English, the source locale).
func Has(key string) bool {
	_, ok := dicts["en"][key]
	return ok
}

// Err translates errors whose message is an i18n key; others pass through.
func Err(lang string, err error) string {
	if Has(err.Error()) {
		return T(lang, err.Error())
	}
	return err.Error()
}

// FromRequest picks the language: the "lang" cookie, then Accept-Language,
// then English.
func FromRequest(r *http.Request) string {
	if c, err := r.Cookie("lang"); err == nil && Supported(c.Value) {
		return c.Value
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		if base := strings.SplitN(tag, "-", 2)[0]; Supported(base) {
			return base
		}
	}
	return "en"
}
