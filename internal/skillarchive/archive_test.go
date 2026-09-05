package skillarchive

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validSkill = "---\nname: sample\ndescription: Inspect source code safely.\n---\nRead the supplied files.\n"

type testEntry struct {
	name string
	body string
	mode os.FileMode
}

func makeArchive(t *testing.T, entries ...testEntry) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(file, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "skill.zip")
	if err := os.WriteFile(name, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestInspectAndExtractSingleSkill(t *testing.T) {
	for _, root := range []string{"", "sample/"} {
		t.Run(root, func(t *testing.T) {
			archive := makeArchive(t, testEntry{name: root + "SKILL.md", body: validSkill}, testEntry{name: root + "scripts/check.py", body: "print('not executed')"})
			manifest, err := Inspect(archive)
			if err != nil || manifest.Root != strings.TrimSuffix(root, "/") || manifest.Name != "sample" || manifest.FileCount != 2 {
				t.Fatalf("unexpected manifest: %+v, %v", manifest, err)
			}
			destination := filepath.Join(t.TempDir(), "source")
			resolved, err := Extract(archive, destination)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := filepath.Abs(filepath.Join(destination, filepath.FromSlash(root)))
			if resolved != want {
				t.Fatalf("root = %q, want %q", resolved, want)
			}
			content, err := os.ReadFile(filepath.Join(resolved, "scripts", "check.py"))
			if err != nil || string(content) != "print('not executed')" {
				t.Fatalf("extraction: %q, %v", content, err)
			}
		})
	}
}

func TestInspectAcceptsDefinitionOnly(t *testing.T) {
	archive := makeArchive(t, testEntry{name: "SKILL.md", body: validSkill})
	manifest, err := Inspect(archive)
	if err != nil || manifest.FileCount != 1 {
		t.Fatalf("definition-only skill: %+v, %v", manifest, err)
	}
}

func TestInspectRejectsInvalidPackages(t *testing.T) {
	cases := map[string][]testEntry{
		"empty":                    {},
		"no definition":            {{name: "README.md", body: "hello"}},
		"nested definition":        {{name: "a/b/SKILL.md", body: validSkill}},
		"multiple skills":          {{name: "a/SKILL.md", body: validSkill}, {name: "b/SKILL.md", body: validSkill}},
		"outside root":             {{name: "a/SKILL.md", body: validSkill}, {name: "README.md", body: "hello"}},
		"duplicate":                {{name: "SKILL.md", body: validSkill}, {name: "SKILL.md", body: validSkill}},
		"case collision":           {{name: "SKILL.md", body: validSkill}, {name: "skill.md", body: validSkill}},
		"case directory collision": {{name: "SKILL.md", body: validSkill}, {name: "Scripts/a.py"}, {name: "scripts/b.py"}},
		"parent traversal":         {{name: "SKILL.md", body: validSkill}, {name: "../outside"}},
		"absolute":                 {{name: "SKILL.md", body: validSkill}, {name: "/outside"}},
		"drive":                    {{name: "SKILL.md", body: validSkill}, {name: "C:/outside"}},
		"backslash":                {{name: "SKILL.md", body: validSkill}, {name: "scripts\\bad.py"}},
		"dot segment":              {{name: "SKILL.md", body: validSkill}, {name: "scripts/./bad.py"}},
		"empty segment":            {{name: "SKILL.md", body: validSkill}, {name: "scripts//bad.py"}},
		"symlink":                  {{name: "SKILL.md", body: validSkill}, {name: "link", body: "/tmp", mode: os.ModeSymlink | 0777}},
		"directory symlink":        {{name: "SKILL.md", body: validSkill}, {name: "scripts/", mode: os.ModeSymlink | 0755}},
		"special file":             {{name: "SKILL.md", body: validSkill}, {name: "pipe", mode: os.ModeNamedPipe | 0600}},
		"file directory collision": {{name: "SKILL.md", body: validSkill}, {name: "scripts", body: "x"}, {name: "scripts/a.py"}},
		"reserved name":            {{name: "SKILL.md", body: validSkill}, {name: "CON.txt", body: "x"}},
		"trailing dot":             {{name: "SKILL.md", body: validSkill}, {name: "bad.", body: "x"}},
		"no frontmatter":           {{name: "SKILL.md", body: "hello"}},
		"empty metadata":           {{name: "SKILL.md", body: "---\n{}\n---\nhello"}},
		"duplicate metadata":       {{name: "SKILL.md", body: "---\nname: first\nname: second\ndescription: yes\n---\nhello"}},
		"invalid metadata type":    {{name: "SKILL.md", body: "---\nname: 12\ndescription: sample\n---\nhello"}},
		"metadata trailing syntax": {{name: "SKILL.md", body: "---\nname: sample\ndescription: safe\n...\nthis: [invalid\n---\nhello"}},
		"metadata trailing object": {{name: "SKILL.md", body: "---\nname: sample\ndescription: safe\n...\nname: extra\n---\nhello"}},
		"invalid UTF8":             {{name: "SKILL.md", body: validSkill + "\xff"}},
		"long name":                {{name: "SKILL.md", body: "---\nname: " + strings.Repeat("x", 129) + "\ndescription: sample\n---\nhello"}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			archive := makeArchive(t, entries...)
			if _, err := Inspect(archive); err == nil {
				t.Fatal("expected invalid package")
			}
			destination := filepath.Join(t.TempDir(), "source")
			if _, err := Extract(archive, destination); err == nil {
				t.Fatal("invalid package was extracted")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("rejected archive should not create destination: %v", err)
			}
		})
	}
}

func TestInspectEnforcesResourceLimits(t *testing.T) {
	t.Run("implicit directory count", func(t *testing.T) {
		entries := []testEntry{{name: "SKILL.md", body: validSkill}}
		for i := 0; i < 999; i++ {
			entries = append(entries, testEntry{name: fmt.Sprintf("d%04d/note.txt", i)})
		}
		if _, err := Inspect(makeArchive(t, entries...)); err != nil {
			t.Fatalf("1999 normalized entries should be valid: %v", err)
		}
		entries = append(entries, testEntry{name: "d0999/note.txt"})
		if _, err := Inspect(makeArchive(t, entries...)); err == nil {
			t.Fatal("accepted more than 2000 normalized files and directories")
		}
	})
	t.Run("file bytes", func(t *testing.T) {
		archive := makeArchive(t, testEntry{name: "SKILL.md", body: validSkill}, testEntry{name: "large.txt", body: strings.Repeat("x", 5<<20+1)})
		if _, err := Inspect(archive); err == nil {
			t.Fatal("accepted oversized file")
		}
	})
	t.Run("total bytes", func(t *testing.T) {
		entries := []testEntry{{name: "SKILL.md", body: validSkill}}
		body := strings.Repeat("x", 5<<20)
		for i := 0; i < 20; i++ {
			entries = append(entries, testEntry{name: string(rune('a'+i)) + ".txt", body: body})
		}
		if _, err := Inspect(makeArchive(t, entries...)); err == nil {
			t.Fatal("accepted oversized archive contents")
		}
	})
	t.Run("entry count", func(t *testing.T) {
		entries := []testEntry{{name: "SKILL.md", body: validSkill}}
		for i := 0; i < 2000; i++ {
			entries = append(entries, testEntry{name: "file-" + strings.Repeat("a", i/100+1) + string(rune(0x4e00+i))})
		}
		if _, err := Inspect(makeArchive(t, entries...)); err == nil {
			t.Fatal("accepted too many entries")
		}
	})
	t.Run("compressed bytes", func(t *testing.T) {
		archive := makeArchive(t, testEntry{name: "SKILL.md", body: validSkill})
		if err := os.Truncate(archive, 20<<20+1); err != nil {
			t.Fatal(err)
		}
		if _, err := Inspect(archive); err == nil {
			t.Fatal("accepted oversized compressed archive")
		}
	})
}

func TestInspectDetectsCorruptContent(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, _ := writer.CreateHeader(&zip.FileHeader{Name: "SKILL.md", Method: zip.Store})
	_, _ = io.WriteString(file, validSkill)
	_ = writer.Close()
	data := buffer.Bytes()
	index := bytes.Index(data, []byte("sample"))
	data[index] = 'X'
	archive := filepath.Join(t.TempDir(), "corrupt.zip")
	_ = os.WriteFile(archive, data, 0600)
	if _, err := Inspect(archive); err == nil {
		t.Fatal("accepted CRC mismatch")
	}
}

func TestExtractRefusesExistingContents(t *testing.T) {
	archive := makeArchive(t, testEntry{name: "SKILL.md", body: validSkill})
	destination := t.TempDir()
	marker := filepath.Join(destination, "keep")
	_ = os.WriteFile(marker, []byte("preserve"), 0600)
	if _, err := Extract(archive, destination); err == nil {
		t.Fatal("accepted nonempty destination")
	}
	data, _ := os.ReadFile(marker)
	if string(data) != "preserve" {
		t.Fatal("modified existing files")
	}
}

func TestInspectDetectsCorruptDirectory(t *testing.T) {
	archive := makeArchive(t, testEntry{name: "SKILL.md", body: validSkill}, testEntry{name: "scripts/", mode: os.ModeDir | 0755})
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	// 修改目录中央记录的 CRC；空目录也必须完整验证其流。
	index := bytes.LastIndex(data, []byte{'P', 'K', 1, 2})
	if index < 0 {
		t.Fatal("missing directory central record")
	}
	binary.LittleEndian.PutUint32(data[index+16:index+20], 1)
	if _, err := InspectReader(bytes.NewReader(data), int64(len(data))); err == nil {
		t.Fatal("accepted directory CRC mismatch")
	}
}
