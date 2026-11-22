package search

import (
	"fmt"
	"strings"
	"testing"

	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

// A realistic build: 10k notes of about 300 words each plus 5k commands, like the library benchmark.
func benchEntities() (notes, cmds []domain.Entity) {
	words := strings.Fields("kernel socket packet handshake payload buffer privilege escalation enumerate banner fingerprint hash salt cipher token session cookie header query shell pipe signal process daemon mount inode symlink checksum archive exploit patch advisory")
	seed := 7
	next := func(n int) int { seed = (seed*1103515245 + 12345) & 0x7fffffff; return seed % n }
	for i := 0; i < 10000; i++ {
		var b strings.Builder
		for j := 0; j < 270; j++ {
			b.WriteString(words[next(len(words))])
			b.WriteByte(' ')
		}
		b.WriteString(fmt.Sprintf("\n```bash\nnmap -sV -p %d host%d\n```\n", next(65000), i))
		notes = append(notes, domain.Entity{"id": fmt.Sprint("n", i), "title": fmt.Sprint("Note ", i), "tags": []any{"t"}, "markdown": b.String(), "kind": "general", "category": ""})
	}
	for i := 0; i < 5000; i++ {
		cmds = append(cmds, domain.Entity{"id": fmt.Sprint("c", i), "title": fmt.Sprint("cmd", i), "name": fmt.Sprint("cmd", i), "tags": []any{},
			"whatItDoes": words[next(len(words))] + " " + words[next(len(words))], "syntax": fmt.Sprint("cmd", i, " [flags] <target>"), "category": "General"})
	}
	return
}

func BenchmarkBuildIndex(b *testing.B) {
	notes, cmds := benchEntities()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix := NewIndex()
		ix.Rebuild("notes", notes)
		ix.Rebuild("commands", cmds)
	}
}
