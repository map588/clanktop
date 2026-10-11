package process

import (
	"bufio"
	"bytes"
	"errors"
	"os/exec"
	"strconv"
	"strings"

	"github.com/map588/clanktop/internal/model"
)

// OpenFiles lists the regular files that the processes hold open, with one
// lsof call. The agent process itself holds no project files open: it opens
// and closes each file within milliseconds. Its children (servers, test
// runners, databases) can hold files open for a long time.
func OpenFiles(pids []int32) ([]model.OpenFile, error) {
	if len(pids) == 0 {
		return nil, nil
	}
	list := make([]string, len(pids))
	for i, p := range pids {
		list[i] = strconv.Itoa(int(p))
	}
	out, err := exec.Command("lsof", "-nP", "-w", "-F", "pcftn", "-p", strings.Join(list, ",")).Output()
	// lsof exits with 1 when one of the PIDs has exited. The output is still valid.
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return nil, err
	}
	return parseLsof(out), nil
}

// parseLsof parses `lsof -F pcftn` output. It keeps regular files that the
// process opened (not its executable or mapped libraries), one per PID and path.
func parseLsof(out []byte) []model.OpenFile {
	var files []model.OpenFile
	seen := map[model.OpenFile]bool{}
	var pid int32
	var proc, fd, typ string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		v := line[1:]
		switch line[0] {
		case 'p':
			n, _ := strconv.Atoi(v)
			pid, proc, fd, typ = int32(n), "", "", ""
		case 'c':
			proc = v
		case 'f':
			fd, typ = v, ""
		case 't':
			typ = v
		case 'n':
			if typ != "REG" || fd == "" || fd[0] < '0' || fd[0] > '9' {
				continue
			}
			f := model.OpenFile{PID: pid, Proc: proc, Path: v}
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	return files
}
