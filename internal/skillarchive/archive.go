// Package skillarchive 校验并提取单个 Skill ZIP；平台与 Agent 共用相同的路径、元数据和资源限制。
package skillarchive

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	MaxArchiveBytes int64 = 20 << 20
	MaxTotalBytes   int64 = 100 << 20
	MaxFileBytes    int64 = 5 << 20
	MaxEntries            = 2000
)

var ErrInvalid = errors.New("invalid skill archive")

// Manifest 仅用于内部处理，不包含可直接投影到浏览器的原始包内容。
type Manifest struct {
	Root      string
	Name      string
	FileCount int
}

func Inspect(filename string) (Manifest, error) {
	file, size, err := openRegularArchive(filename)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	return InspectReader(file, size)
}

// InspectReader 允许附件服务校验已检查所有权和文件身份的同一个打开句柄。
func InspectReader(reader io.ReaderAt, size int64) (Manifest, error) {
	manifest, _, err := inspect(reader, size)
	return manifest, err
}

func openRegularArchive(filename string) (*os.File, int64, error) {
	before, err := os.Lstat(filename)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > MaxArchiveBytes {
		return nil, 0, ErrInvalid
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, 0, ErrInvalid
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || before.Size() != after.Size() {
		_ = file.Close()
		return nil, 0, ErrInvalid
	}
	return file, after.Size(), nil
}

func inspect(reader io.ReaderAt, size int64) (Manifest, *zip.Reader, error) {
	if size <= 0 || size > MaxArchiveBytes {
		return Manifest{}, nil, ErrInvalid
	}
	archive, err := zip.NewReader(reader, size)
	if err != nil || len(archive.File) == 0 || len(archive.File) > MaxEntries {
		return Manifest{}, nil, ErrInvalid
	}
	var manifest Manifest
	var definition []byte
	var definitionPath string
	var total int64
	// 显式及隐式目录一并登记，防止跨平台大小写差异和文件/目录覆盖。
	type entryKind struct {
		spelling            string
		directory, explicit bool
	}
	seen := map[string]entryKind{}
	for _, file := range archive.File {
		directory := file.FileInfo().IsDir()
		name := strings.TrimSuffix(file.Name, "/")
		if !validPath(name) || (strings.HasSuffix(file.Name, "/") != directory) ||
			(!directory && !file.Mode().IsRegular()) || file.Flags&1 != 0 ||
			(directory && (file.Mode().Type() != os.ModeDir || file.UncompressedSize64 != 0)) ||
			(file.Method != zip.Store && file.Method != zip.Deflate) || file.UncompressedSize64 > uint64(MaxFileBytes) {
			return Manifest{}, nil, ErrInvalid
		}
		parts := strings.Split(name, "/")
		for i := range parts {
			prefix := strings.Join(parts[:i+1], "/")
			key := strings.ToLower(prefix)
			isLast := i == len(parts)-1
			isDirectory := !isLast || directory
			if previous, exists := seen[key]; exists {
				if previous.spelling != prefix || previous.directory != isDirectory || isLast && previous.explicit {
					return Manifest{}, nil, ErrInvalid
				}
				if isLast {
					previous.explicit = true
					seen[key] = previous
				}
			} else {
				seen[key] = entryKind{prefix, isDirectory, isLast}
				if len(seen) > MaxEntries {
					return Manifest{}, nil, ErrInvalid
				}
			}
		}
		// zip.File.Open 会直接跳过以斜杠结尾的目录流；副本去掉尾斜杠，确保目录也验证解压与 CRC。
		checkedEntry := *file
		checkedEntry.Name = name
		stream, err := checkedEntry.Open()
		if err != nil {
			return Manifest{}, nil, ErrInvalid
		}
		limited := io.LimitReader(stream, MaxFileBytes+1)
		var count int64
		if !directory && path.Base(name) == "SKILL.md" {
			if definitionPath != "" || len(parts) > 2 {
				_ = stream.Close()
				return Manifest{}, nil, ErrInvalid
			}
			definition, err = io.ReadAll(limited)
			count = int64(len(definition))
			definitionPath = name
		} else {
			count, err = io.Copy(io.Discard, limited)
		}
		closeErr := stream.Close()
		total += count
		if err != nil || closeErr != nil || count > MaxFileBytes || uint64(count) != file.UncompressedSize64 || total > MaxTotalBytes {
			return Manifest{}, nil, ErrInvalid
		}
		if !directory {
			manifest.FileCount++
		}
	}
	if definitionPath == "" {
		return Manifest{}, nil, ErrInvalid
	}
	if root := path.Dir(definitionPath); root != "." {
		manifest.Root = root
	}
	if manifest.Root != "" {
		for _, file := range archive.File {
			name := strings.TrimSuffix(file.Name, "/")
			if name != manifest.Root && !strings.HasPrefix(name, manifest.Root+"/") {
				return Manifest{}, nil, ErrInvalid
			}
		}
	}
	manifest.Name, err = skillName(definition)
	if err != nil {
		return Manifest{}, nil, ErrInvalid
	}
	return manifest, archive, nil
}

func validPath(name string) bool {
	if name == "" || len(name) > 1024 || !utf8.ValidString(name) || strings.ContainsAny(name, "\\<>:\"|?*") {
		return false
	}
	for _, character := range name {
		if character < 32 || character == 127 {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || len(part) > 255 || strings.HasSuffix(part, " ") || strings.HasSuffix(part, ".") {
			return false
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
			(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
			return false
		}
	}
	return true
}

func skillName(content []byte) (string, error) {
	if !utf8.Valid(content) {
		return "", ErrInvalid
	}
	text := strings.TrimPrefix(strings.ReplaceAll(string(content), "\r\n", "\n"), "\ufeff")
	lines := strings.Split(text, "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return "", ErrInvalid
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return "", ErrInvalid
	}
	metadata := strings.Join(lines[1:end], "\n")
	if len(metadata) > 64<<10 {
		return "", ErrInvalid
	}
	var fields map[string]any
	decoder := yaml.NewDecoder(strings.NewReader(metadata))
	if err := decoder.Decode(&fields); err != nil {
		return "", ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", ErrInvalid
	}
	name, nameOK := fields["name"].(string)
	description, descriptionOK := fields["description"].(string)
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if !nameOK || !descriptionOK || name == "" || description == "" || utf8.RuneCountInString(name) > 128 || utf8.RuneCountInString(description) > 2000 {
		return "", ErrInvalid
	}
	return name, nil
}

// Extract 先完整校验再写入全新目录，禁止覆盖已有内容或沿符号链接提取。
func Extract(filename, destination string) (root string, resultErr error) {
	file, size, err := openRegularArchive(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	manifest, archive, err := inspect(file, size)
	if err != nil {
		return "", err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return "", ErrInvalid
	}
	if _, err = os.Lstat(destination); !os.IsNotExist(err) {
		return "", ErrInvalid
	}
	if err = os.Mkdir(destination, 0700); err != nil {
		return "", ErrInvalid
	}
	defer func() {
		if resultErr != nil {
			_ = os.RemoveAll(destination)
		}
	}()
	var total int64
	for _, entry := range archive.File {
		target := filepath.Join(destination, filepath.FromSlash(entry.Name))
		if entry.FileInfo().IsDir() {
			if err = os.MkdirAll(target, 0700); err != nil {
				return "", ErrInvalid
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return "", ErrInvalid
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return "", ErrInvalid
		}
		input, err := entry.Open()
		if err != nil {
			_ = output.Close()
			return "", ErrInvalid
		}
		count, copyErr := io.Copy(output, io.LimitReader(input, MaxFileBytes+1))
		inputErr, outputErr := input.Close(), output.Close()
		total += count
		if copyErr != nil || inputErr != nil || outputErr != nil || count > MaxFileBytes || total > MaxTotalBytes || uint64(count) != entry.UncompressedSize64 {
			return "", ErrInvalid
		}
	}
	return filepath.Join(destination, filepath.FromSlash(manifest.Root)), nil
}
