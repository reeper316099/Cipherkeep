package csvx

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tb := Parse("\xef\xbb\xbfName,Notes\r\n\"a, b\",\"he said \"\"hi\"\"\nsecond line\"\r\n\r\nx\r\n", Options{SkipEmpty: true})
	if !reflect.DeepEqual(tb.Headers, []string{"Name", "Notes"}) {
		t.Fatalf("headers %#v", tb.Headers)
	}
	want := [][]string{{"a, b", "he said \"hi\"\nsecond line"}, {"x"}}
	if !reflect.DeepEqual(tb.Rows, want) {
		t.Fatalf("rows %#v", tb.Rows)
	}
}

func TestDelimiterAndDuplicates(t *testing.T) {
	tb := Parse("a;a;b\n1;2;3\n4;5;6", Options{})
	if !reflect.DeepEqual(tb.Headers, []string{"a", "a_1", "b"}) || len(tb.Rows) != 2 || tb.Rows[1][2] != "6" {
		t.Fatalf("%#v", tb)
	}
	if got := Parse("single\nvalue", Options{}); got.Headers[0] != "single" {
		t.Fatalf("%#v", got)
	}
}

func TestMaxRowsAndBlanks(t *testing.T) {
	tb := Parse("h\n1\n2\n3", Options{MaxRows: 1})
	if len(tb.Rows) != 1 {
		t.Fatalf("%#v", tb)
	}
	if n := len(Parse("h\n\n \n1", Options{SkipEmpty: true}).Rows); n != 1 {
		t.Fatalf("greedy skip kept %d rows", n)
	}
	if n := len(Parse("h\n\n1", Options{}).Rows); n != 2 {
		t.Fatalf("no skip kept %d rows", n)
	}
}

func TestUnparse(t *testing.T) {
	got := Unparse([]string{"a", "b c"}, [][]string{{"x,y", "q\"r"}, {" lead", ""}, {"line\nbreak", "ok"}})
	want := "a,b c\n\"x,y\",\"q\"\"r\"\n\" lead\",\n\"line\nbreak\",ok"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if Unparse([]string{"a", "b"}, nil) != "a,b" {
		t.Fatal("header only")
	}
}
