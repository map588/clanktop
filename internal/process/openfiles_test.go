package process

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/map588/clanktop/internal/model"
)

// testdata/lsof.txt is real `lsof -F pcftn` output for a node MCP server and
// tokensave, with the home directory replaced.
func TestParseLsof(t *testing.T) {
	out, err := os.ReadFile(filepath.Join("testdata", "lsof.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := parseLsof(out)
	want := []model.OpenFile{
		{PID: 88764, Proc: "tokensave", Path: "/Users/x/git/tui/clanktop/.tokensave/tokensave.db"},
		{PID: 88764, Proc: "tokensave", Path: "/Users/x/git/tui/clanktop/.tokensave/tokensave.db-wal"},
		{PID: 88764, Proc: "tokensave", Path: "/Users/x/git/tui/clanktop/.tokensave/tokensave.db-shm"},
		{PID: 88764, Proc: "tokensave", Path: "/Users/x/.tokensave/global.db"},
		{PID: 88764, Proc: "tokensave", Path: "/Users/x/.tokensave/global.db-wal"},
		{PID: 88764, Proc: "tokensave", Path: "/Users/x/.tokensave/global.db-shm"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestOpenFilesOfSelf(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "open")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	path, _ := filepath.EvalSymlinks(f.Name())
	files, err := OpenFiles([]int32{int32(os.Getpid()), 999999})
	if err != nil {
		t.Fatal(err)
	}
	for _, of := range files {
		if of.Path == path {
			return
		}
	}
	t.Fatalf("%s not in %+v", path, files)
}
