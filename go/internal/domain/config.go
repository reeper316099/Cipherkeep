package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Settings are the user's preferences (config/settings.json).
type Settings struct {
	Theme               string   `json:"theme"`
	Accent              string   `json:"accent"`
	Density             string   `json:"density"`
	StoreFlagsInLabs    bool     `json:"storeFlagsInLabs"`
	BlurSensitive       bool     `json:"blurSensitive"`
	NotionAPIEnabled    bool     `json:"notionApiEnabled"`
	VimMode             bool     `json:"vimMode"`
	BackupKeep          int      `json:"backupKeep"`
	BackupIntervalHours float64  `json:"backupIntervalHours"`
	StudyNewPerDay      int      `json:"studyNewPerDay"`
	SanitizePatterns    []string `json:"sanitizePatterns"`
}

// DefaultSettings returns the defaults.
func DefaultSettings() Settings {
	return Settings{Theme: "dark", Accent: "#38e8c6", Density: "comfortable", BackupKeep: 20, BackupIntervalHours: 6,
		StudyNewPerDay: 10, SanitizePatterns: []string{}}
}

// UnmarshalJSON starts from the defaults, so a settings file that predates a key still loads (unknown keys are ignored
// here; ParseSettings is the strict reader used for input).
func (s *Settings) UnmarshalJSON(b []byte) error {
	*s = DefaultSettings()
	type plain Settings
	return json.Unmarshal(b, (*plain)(s))
}

// Validate checks the ranges and fills lists so they never serialise as null.
func (s *Settings) Validate() error {
	if s.SanitizePatterns == nil {
		s.SanitizePatterns = []string{}
	}
	switch {
	case s.Theme != "dark" && s.Theme != "light" && s.Theme != "system":
		return errors.New("theme must be dark, light or system")
	case s.Density != "comfortable" && s.Density != "compact":
		return errors.New("density must be comfortable or compact")
	case s.BackupKeep < 1 || s.BackupKeep > 200:
		return errors.New("backupKeep must be between 1 and 200")
	case s.BackupIntervalHours < 1 || s.BackupIntervalHours > 168:
		return errors.New("backupIntervalHours must be between 1 and 168")
	case s.StudyNewPerDay < 0 || s.StudyNewPerDay > 100:
		return errors.New("studyNewPerDay must be between 0 and 100")
	case len(s.SanitizePatterns) > 30:
		return errors.New("at most 30 sanitize patterns")
	}
	for _, p := range s.SanitizePatterns {
		if len(p) < 1 || len(p) > 200 {
			return errors.New("each sanitize pattern must be 1 to 200 characters")
		}
	}
	return nil
}

// ParseSettings decodes a settings object strictly (unknown keys are rejected), applying defaults for missing keys.
func ParseSettings(raw []byte) (Settings, error) {
	s := DefaultSettings()
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	type plain Settings // bypasses the lenient UnmarshalJSON so unknown keys are caught
	if err := dec.Decode((*plain)(&s)); err != nil {
		return s, err
	}
	return s, s.Validate()
}

// Option is one allowed value of a taxonomy list.
type Option struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// Categories maps a collection to its category options.
type Categories map[string][]Option

// DefaultCategories are the seed options.
func DefaultCategories() Categories {
	names := func(xs ...string) []Option {
		out := make([]Option, len(xs))
		for i, x := range xs {
			out[i] = Option{Name: x}
		}
		return out
	}
	return Categories{
		"commands": names("File System", "Networking", "Permissions", "Text Processing", "General"),
		"concepts": names("Linux", "Bash", "Networking", "Web Security"),
		"labs":     names("picoCTF", "TryHackMe", "Bandit"),
		"projects": names("Website", "Tool", "Script"),
	}
}

// Validate checks every option has a name.
func (c Categories) Validate() error {
	for coll, opts := range c {
		if opts == nil {
			return fmt.Errorf("%s: expected a list", coll)
		}
		for _, o := range opts {
			if o.Name == "" {
				return fmt.Errorf("%s: option names cannot be empty", coll)
			}
		}
	}
	return nil
}

// SkillDomain is one domain of the skill map.
type SkillDomain struct {
	Name   string  `json:"name"`
	Weight float64 `json:"weight"`
	// Aliases are category, tag or domain labels that count towards this domain (case-insensitive).
	Aliases []string `json:"aliases"`
}

// DefaultSkillDomains is the starting skill map.
var DefaultSkillDomains = []SkillDomain{
	{"Linux", 1, []string{"linux", "file system", "general", "permissions"}},
	{"Bash", 1, []string{"bash", "shell", "text processing"}},
	{"Networking", 1, []string{"networking", "network", "tcp/ip"}},
	{"Web Security", 1, []string{"web security", "web", "xss", "sqli"}},
	{"Windows", 1, []string{"windows", "powershell"}},
	{"Cryptography", 1, []string{"cryptography", "crypto"}},
	{"Forensics", 1, []string{"forensics"}},
	{"OSINT", 1, []string{"osint"}},
	{"Log Analysis", 1, []string{"log analysis", "logs"}},
	{"Scanning & Recon", 1, []string{"scanning & recon", "scanning", "recon", "reconnaissance"}},
	{"Password Cracking", 1, []string{"password cracking", "passwords"}},
	{"Reverse Engineering", 1, []string{"reverse engineering", "reversing", "rev"}},
	{"Binary Exploitation", 1, []string{"binary exploitation", "pwn"}},
}

// DefaultSkills returns a copy of the default skill map.
func DefaultSkills() []SkillDomain {
	out := make([]SkillDomain, len(DefaultSkillDomains))
	for i, d := range DefaultSkillDomains {
		out[i] = SkillDomain{Name: d.Name, Weight: d.Weight, Aliases: append([]string{}, d.Aliases...)}
	}
	return out
}

// ValidateSkills checks names and weights and fills nil alias lists.
func ValidateSkills(s *[]SkillDomain) error {
	if *s == nil {
		*s = []SkillDomain{}
	}
	for i := range *s {
		d := &(*s)[i]
		if d.Name == "" {
			return errors.New("skill names cannot be empty")
		}
		if d.Weight < 0 || d.Weight > 10 {
			return fmt.Errorf("%s: weight must be between 0 and 10", d.Name)
		}
		if d.Aliases == nil {
			d.Aliases = []string{}
		}
	}
	return nil
}

// MigrateSkillsV1 gives v1 skills (which had no aliases) the default aliases of the domain with the same name, so the
// skill map works on existing data.
func MigrateSkillsV1(raw json.RawMessage) (json.RawMessage, error) {
	var list []struct {
		Name    string   `json:"name"`
		Weight  *float64 `json:"weight"`
		Aliases []string `json:"aliases"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	out := make([]SkillDomain, 0, len(list))
	for _, s := range list {
		w := 1.0
		if s.Weight != nil {
			w = *s.Weight
		}
		aliases := s.Aliases
		if aliases == nil {
			aliases = []string{}
			for _, d := range DefaultSkillDomains {
				if strings.EqualFold(d.Name, s.Name) {
					aliases = append([]string{}, d.Aliases...)
					break
				}
			}
		}
		out = append(out, SkillDomain{Name: s.Name, Weight: w, Aliases: aliases})
	}
	return json.Marshal(out)
}

// TemplateSection is one heading of a page template.
type TemplateSection struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Hint  string `json:"hint,omitempty"`
}

// Template is a page template.
type Template struct {
	Name     string            `json:"name"`
	Sections []TemplateSection `json:"sections"`
}

var (
	leadSymbols = regexp.MustCompile(`^[^\p{L}\p{N}]+`)
	keyNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)
)

// SectionKey derives a stable key from a heading ("🧠 What is it?" -> "what-is-it").
func SectionKey(title string) string {
	k := leadSymbols.ReplaceAllString(title, "")
	k = strings.ToLower(k)
	k = keyNonAlnum.ReplaceAllString(k, "-")
	return strings.TrimSuffix(k, "-")
}

func tmpl(name string, titles ...string) Template {
	t := Template{Name: name}
	for _, title := range titles {
		t.Sections = append(t.Sections, TemplateSection{Key: SectionKey(title), Title: title})
	}
	return t
}

// DefaultTemplates returns the built-in page templates by collection.
func DefaultTemplates() map[string]Template {
	return map[string]Template{
		"commands": tmpl("Command", "📌 What it does", "⚙️ Basic Syntax", "🧩 Arguments", "🔧 Common Options (Flags)", "💡 Examples"),
		"concepts": tmpl("Cyber Concept", "🧠 What is it?", "⚙️ How it works (Step-by-step)", "🧩 Key Components",
			"🔁 Example / Walkthrough", "💡 Why it matters", "⚔️ Security Relevance", "🛠️ Tools / Commands Related",
			"🔗 Related Concepts", "⚠️ Common Mistakes / Misconceptions", "🧠 Intuition (VERY IMPORTANT)",
			"📚 Source / Where I learned this", "🔥 Difficulty Level", "❓ Questions I still have"),
		"labs": tmpl("Lab", "🎯 Goal", "🔍 Process", "💥 Solution", "🧠 What I learned", "🛠️ Commands Used"),
		"log":  tmpl("Learning Log", "📚 What I learned", "⚡ Key insight", "❓ Questions", "🔁 Review later"),
	}
}
