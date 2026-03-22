package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

//go:embed locales/*.json
var locales embed.FS

// SupportedLangs matches the list in dashboard i18n.js
var SupportedLangs = []string{
	"ar", "bn", "cs", "de", "en", "es", "fr", "he", "hi", "id", "it", "ja", "ko",
	"ms", "nl", "pl", "pt", "ro", "ru", "sv", "th", "tr", "uk", "ur", "vi", "zh",
}

type Translator struct {
	Lang           string
	translations   map[string]string
	enTranslations map[string]string
}

// NewTranslator initializes a translator with the given language.
// If the language is not supported, it falls back to English.
func NewTranslator(lang string) *Translator {
	if lang == "" {
		lang = DetectLang()
	}

	supported := false
	for _, l := range SupportedLangs {
		if l == lang {
			supported = true
			break
		}
	}
	if !supported {
		lang = "en"
	}

	t := &Translator{Lang: lang}
	t.load()
	return t
}

func (t *Translator) load() {
	// Always load English as a fallback
	enData, _ := locales.ReadFile("locales/en.json")
	_ = json.Unmarshal(enData, &t.enTranslations)

	if t.Lang == "en" {
		t.translations = t.enTranslations
		return
	}

	data, err := locales.ReadFile(fmt.Sprintf("locales/%s.json", t.Lang))
	if err == nil {
		_ = json.Unmarshal(data, &t.translations)
	} else {
		// If the specific language file fails to load, fallback to English
		t.translations = t.enTranslations
	}
}

// T returns the translation for the given key.
// It checks the active language first, then falls back to English,
// and finally returns the key itself if not found.
func (t *Translator) T(key string) string {
	if val, ok := t.translations[key]; ok {
		return val
	}
	if val, ok := t.enTranslations[key]; ok {
		return val
	}
	return key
}

// DetectLang tries to determine the system language from environment variables.
func DetectLang() string {
	lang := os.Getenv("LC_ALL")
	if lang == "" {
		lang = os.Getenv("LC_MESSAGES")
	}
	if lang == "" {
		lang = os.Getenv("LANG")
	}
	if lang == "" {
		return "en"
	}

	// Examples: en_US.UTF-8, pl_PL, fr, etc.
	parts := strings.Split(lang, ".")
	base := parts[0]
	parts = strings.Split(base, "_")
	return strings.ToLower(parts[0])
}

// GetRawLocale returns the raw JSON bytes for a specific language.
// Useful for the web server to serve translations to the frontend.
func GetRawLocale(lang string) ([]byte, error) {
	return locales.ReadFile(fmt.Sprintf("locales/%s.json", lang))
}
