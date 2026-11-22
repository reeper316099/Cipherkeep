package search

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

// Fields that are indexed, in order, with their boosts.
const (
	fName = iota
	fTitle
	fTags
	fBody
	fCode
	numFields
)

var boosts = [numFields]float64{12, 6, 3, 1, 2}

// Doc is what is indexed for one entity.
type Doc struct {
	Key, Type, ID                                   string
	Name, Title, Tags, Body, Code, Category, Status string
}

var fenced = regexp.MustCompile("(?s)```[^\n]*\n(.*?)```")

func fencedCode(md string) string {
	var parts []string
	for _, m := range fenced.FindAllStringSubmatch(md, -1) {
		parts = append(parts, m[1])
	}
	return strings.Join(parts, "\n")
}

func joinStrs(xs ...string) string { return strings.Join(xs, "\n") }

// ToDoc builds the searchable document of an entity.
func ToDoc(typ string, e domain.Entity) Doc {
	d := Doc{
		Key: typ + ":" + e.ID(), Type: typ, ID: e.ID(), Title: e.Title(),
		Tags: strings.Join(e.Strs("tags"), " "), Category: e.Str("category"),
	}
	switch typ {
	case "commands":
		d.Name = e.Str("name")
		body := []string{e.Str("whatItDoes"), e.Str("explanation"), e.Str("syntax"), e.Str("notes")}
		for _, a := range e.Objs("arguments") {
			body = append(body, fmt.Sprintf("%s %s", str(a["name"]), str(a["meaning"])))
		}
		for _, o := range e.Objs("options") {
			body = append(body, str(o["meaning"]))
		}
		for _, x := range e.Objs("examples") {
			body = append(body, str(x["description"]))
		}
		d.Body = joinStrs(body...)
		code := []string{e.Str("syntax")}
		for _, o := range e.Objs("options") {
			code = append(code, str(o["flag"]))
		}
		for _, x := range e.Objs("examples") {
			code = append(code, str(x["command"]))
		}
		d.Code = joinStrs(code...)
	case "concepts":
		var parts []string
		for _, s := range e.Objs("sections") {
			parts = append(parts, str(s["title"])+"\n"+str(s["markdown"]))
		}
		d.Body = strings.Join(parts, "\n")
		d.Code = fencedCode(d.Body)
		d.Status = e.Str("difficulty")
	case "labs":
		d.Body = joinStrs(e.Str("writeup"), e.Str("domain"))
		d.Code = fencedCode(e.Str("writeup"))
		d.Status = e.Str("status")
	case "projects":
		d.Body = joinStrs(append([]string{e.Str("notes")}, e.Strs("skillsUsed")...)...)
		d.Status = e.Str("status")
	case "log":
		d.Body = joinStrs(append([]string{e.Str("keyTakeaway"), e.Str("topicsRaw"), e.Str("body")}, e.Strs("topics")...)...)
	case "notes":
		d.Body = e.Str("markdown")
		d.Code = fencedCode(d.Body)
		d.Status = e.Str("kind")
	case "snippets":
		d.Body = e.Str("description")
		d.Code = e.Str("code")
		d.Status = e.Str("language")
	}
	return d
}

func str(v any) string { s, _ := v.(string); return s }

func (d *Doc) field(i int) string {
	switch i {
	case fName:
		return d.Name
	case fTitle:
		return d.Title
	case fTags:
		return d.Tags
	case fBody:
		return d.Body
	}
	return d.Code
}
