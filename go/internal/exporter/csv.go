package exporter

import "github.com/reeper316099/Cipherkeep/go/internal/csvx"

func csvUnparse(fields []string, rows [][]string) string { return csvx.Unparse(fields, rows) }
