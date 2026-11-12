package config

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// lockedWords put an owner's comment in a locked category (owner-comments-handled): a deterministic,
// conservative list in English and Russian, matched at the start of a word. The owner may also name the
// category with the comment.
var lockedWords = map[string][]string{
	"release":  {"testflight", "app store", "appstore", "play store", "release", "publish", "production", "deploy", "релиз", "опубликуй", "опубликовать", "выкати", "выложи"},
	"money":    {"payment", "pay for", "price", "pricing", "budget", "purchase", "subscription", "refund", "invoice", "billing", "оплат", "деньг", "бюджет", "подписк"},
	"deletion": {"delete", "deletion", "wipe", "erase", "drop the database", "удали", "удаление", "стереть", "сотри"},
	"legal":    {"legal", "license", "licence", "contract", "terms of service", "privacy policy", "gdpr", "trademark", "copyright", "юрид", "лиценз", "договор"},
}

// LockedCategoryOf is the locked category an owner's text falls in, "" for none: the first category in
// LockedCategories order one of whose words starts a word of the text.
func LockedCategoryOf(text string) string {
	lower := strings.ToLower(text)
	for _, category := range LockedCategories {
		for _, w := range lockedWords[category] {
			for from := 0; from < len(lower); {
				i := strings.Index(lower[from:], w)
				if i < 0 {
					break
				}
				i += from
				if r, _ := utf8.DecodeLastRuneInString(lower[:i]); i == 0 || !unicode.IsLetter(r) && !unicode.IsDigit(r) {
					return category
				}
				from = i + len(w)
			}
		}
	}
	return ""
}

// LockedCategory reports whether category is one only the owner decides.
func LockedCategory(category string) bool {
	for _, c := range LockedCategories {
		if c == category {
			return true
		}
	}
	return false
}
