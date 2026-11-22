package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func compileAndRun(t *testing.T, src string, stdin string) string {
	t.Helper()
	goCode, err := compileSource(src)
	if err != nil {
		t.Fatalf("compileSource failed: %v", err)
	}

	tmpDir := t.TempDir()
	tmpGo := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(tmpGo, []byte(goCode), 0644); err != nil {
		t.Fatalf("write temp go: %v", err)
	}

	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	bin := filepath.Join(tmpDir, "prog"+ext)
	build := exec.Command("go", "build", "-o", bin, tmpGo)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s\n--- generated Go ---\n%s", err, string(out), goCode)
	}

	run := exec.Command(bin)
	if stdin != "" {
		run.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	run.Stdout = &stdout
	run.Stderr = &stderr
	if err := run.Run(); err != nil {
		t.Fatalf("run failed: %v\nstderr: %s", err, stderr.String())
	}
	return strings.ReplaceAll(stdout.String(), "\r\n", "\n")
}

func TestV2BackwardsCompat(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("examples", "script.fks"))
	if err != nil {
		t.Fatalf("read script.fks: %v", err)
	}
	got := compileAndRun(t, string(data), "")
	want := "true\nwelcomecreator\n0\n1\n2\n3\n4\nall done\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestLocalScopingAndShadowing(t *testing.T) {
	src := `
import print

let x be 10

fn inner do
    let x be 99
    let unused be 123
    if x is 99 do
        let x be 7
        ln of print contains x
    end
    ln of print contains x
end

exec inner
ln of print contains x
`
	got := compileAndRun(t, src, "")
	want := "7\n99\n10\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPrecedenceAndLogic(t *testing.T) {
	src := `
import print

let a be 2 plus 3 times 4
let b be group 2 plus 3 done times 4
let c be 20 divided by 4 minus 1
let d be 17 mod 5
let e be negative 5 plus 8
ln of print contains a and b and c and d and e

if a is 14 and b is 20 do
    ln of print contains say both ok end
end

if not false and group a is over 10 or b is under 0 done do
    ln of print contains say logic ok end
end
`
	got := compileAndRun(t, src, "")
	want := "14 20 4 2 3\nboth ok\nlogic ok\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestListsAndLoops(t *testing.T) {
	src := `
import print

let nums be list of 10 and 20 and 30 end
set item 1 of nums to 25
append 40 to nums
ln of print contains length of nums and item 1 of nums and item 3 of nums

let sum be 0
for each n in nums do
    set sum to sum plus n
end
ln of print contains sum

let acc be 0
loop from 1 to 10 as i do
    if i mod 2 is 0 do
        skip
    end
    if i is over 7 do
        stop
    end
    set acc to acc plus i
end
ln of print contains acc
`
	got := compileAndRun(t, src, "")
	want := "4 25 40\n105\n16\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStdlibMathTextFile(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "fks_test_file")
	// Escape backslashes aren't needed in say...end since Go quotes with strconv.Quote
	src := `
import print
import math
import text
import file

ln of print contains abs of math contains negative 9 and min of math contains 4 and 9 and max of math contains 4 and 9
ln of print contains pow of math contains 3 and 4 and sqrt of math contains 81 and clamp of math contains 50 and 0 and 10

let s be say   Hello World   end
let cleaned be lower of text contains trim of text contains s
ln of print contains cleaned and has of text contains cleaned and say world end
ln of print contains replace of text contains cleaned and say world end and say fakescript end
ln of print contains fromnum of text contains 42 and tonum of text contains say 99 end

let path be say ` + tmpFile + ` end
write of file contains path and say hello end
append of file contains path and say  world end
ln of print contains exists of file contains path and read of file contains path
`
	got := compileAndRun(t, src, "")
	want := "9 4 9\n81 9 10\nhello world true\nhello fakescript\n42 99\ntrue hello world\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestStandardInputAsk(t *testing.T) {
	src := `
import print

ask say Name  end into user
ask num say Age  end into age
ln of print contains user and age plus 1
`
	got := compileAndRun(t, src, "Ada\n35\n")
	want := "Name Age Ada 36\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGoKeywordEscaping(t *testing.T) {
	src := `
import print

fn range with num type returns num do
    let len be type plus 1
    give back len
end

ln of print contains exec range with 41
`
	got := compileAndRun(t, src, "")
	want := "42\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCompileErrors(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantSub string
	}{
		{
			name:    "rejects punctuation symbol",
			src:     "let x = 5\n",
			wantSub: "not allowed in FakeScript",
		},
		{
			name:    "unterminated string",
			src:     "let s be say hello\n",
			wantSub: "unterminated string literal",
		},
		{
			name:    "type mismatch",
			src:     "let x be 5\nset x to say hi end\n",
			wantSub: "cannot assign text to variable \"x\" of type num",
		},
		{
			name:    "wrong arg count",
			src:     "fn add with num a and num b returns num do\ngive back a plus b\nend\nexec add with 1\n",
			wantSub: "expects 2 argument(s), got 1",
		},
		{
			name:    "stop outside loop",
			src:     "stop\n",
			wantSub: "only be used inside a loop",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compileSource(tc.src)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("expected error containing %q, got %q", tc.wantSub, err.Error())
			}
		})
	}
}
