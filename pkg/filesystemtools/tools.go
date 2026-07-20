package filesystemtools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	toolReadFile     = "read_file"
	toolWriteFile    = "write_file"
	toolListDir      = "list_directory"
	toolFileInfo     = "file_info"
	toolSearchFiles  = "search_files"
	toolReadMultiple = "read_multiple_files"
)

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type ToolHandler func(ctx context.Context, args json.RawMessage, root *os.Root) (interface{}, error)

var filesystemTools = make(map[string]ToolHandler)

func init() {
	filesystemTools[toolReadFile] = handleReadFile
	filesystemTools[toolWriteFile] = handleWriteFile
	filesystemTools[toolListDir] = handleListDir
	filesystemTools[toolFileInfo] = handleFileInfo
	filesystemTools[toolSearchFiles] = handleSearchFiles
	filesystemTools[toolReadMultiple] = handleReadMultiple
}

type FilesystemClient struct {
	logger *slog.Logger
	root   *os.Root
	roots  []string
}

func NewFilesystemClient(logger *slog.Logger, allowedRoots []string) (*FilesystemClient, error) {
	if len(allowedRoots) == 0 {
		return nil, fmt.Errorf("filesystem.allowed_roots is required — set at least one allowed root directory in config")
	}

	roots := make([]string, 0, len(allowedRoots))
	for _, r := range allowedRoots {
		cleaned := filepath.Clean(r)
		abs, err := filepath.Abs(cleaned)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed_root %q: %w", r, err)
		}
		roots = append(roots, abs)
	}

	rootDir := roots[0]
	r, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open root %q: %w", rootDir, err)
	}

	logger.Info("filesystem client initialized",
		"allowed_roots", roots,
		"primary_root", rootDir,
	)

	return &FilesystemClient{
		logger: logger,
		root:   r,
		roots:  roots,
	}, nil
}

func (c *FilesystemClient) AllowedRoots() []string {
	return c.roots
}

func (c *FilesystemClient) Close() error {
	return c.root.Close()
}

func (c *FilesystemClient) GetTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        toolReadFile,
			Description: "Read the contents of a file. For files over 1MB, content is streamed. Path is resolved relative to the allowed roots.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Path to the file (relative to the allowed root)"}
				},
				"required": ["path"]
			}`),
		},
		{
			Name:        toolWriteFile,
			Description: "Write content to a file. Creates parent directories if they don't exist. Path is resolved relative to the allowed roots.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Path to the file (relative to the allowed root)"},
					"content": {"type": "string", "description": "Content to write to the file"}
				},
				"required": ["path", "content"]
			}`),
		},
		{
			Name:        toolListDir,
			Description: "List the contents of a directory. Returns file names, sizes, and whether each entry is a directory.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Directory path (relative to the allowed root)"}
				},
				"required": ["path"]
			}`),
		},
		{
			Name:        toolFileInfo,
			Description: "Get detailed information about a file or directory.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Path to the file or directory (relative to the allowed root)"}
				},
				"required": ["path"]
			}`),
		},
		{
			Name:        toolSearchFiles,
			Description: "Search for files matching a glob pattern within the allowed root.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"pattern": {"type": "string", "description": "Glob pattern to match (e.g. '**/*.go')"},
					"root": {"type": "string", "description": "Root subdirectory to search from (default: '.')"}
				},
				"required": ["pattern"]
			}`),
		},
		{
			Name:        toolReadMultiple,
			Description: "Read multiple files in a single call. All paths are resolved relative to the allowed roots.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"paths": {
						"type": "array",
						"items": {"type": "string"},
						"description": "Array of file paths to read"
					}
				},
				"required": ["paths"]
			}`),
		},
	}
}

func (c *FilesystemClient) CallTool(ctx context.Context, name string, args json.RawMessage) (interface{}, error) {
	handler, ok := filesystemTools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return handler(ctx, args, c.root)
}

// resolvePathWithinRoots checks if the given path is within any allowed root
// and returns the clean path relative to the primary root for os.Root access.
func resolvePathWithinRoots(cleanPath string) (string, error) {
	if cleanPath == "" {
		return "", fmt.Errorf("path must not be empty")
	}

	cleaned := filepath.Clean(cleanPath)
	if cleaned == "" || cleaned == "." {
		return ".", nil
	}

	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("absolute paths are not allowed: %q", cleanPath)
	}

	if strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("path %q attempts to escape the allowed root (path traversal not permitted)", cleanPath)
	}

	for _, component := range strings.Split(cleaned, string(filepath.Separator)) {
		if component == ".." {
			return "", fmt.Errorf("path %q contains '..' which escapes the allowed root", cleanPath)
		}
	}

	return cleaned, nil
}

type ReadFileResult struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated,omitempty"`
}

const maxInlineFileSize = 1 << 20 // 1MB - beyond this, we warn about streaming

func handleReadFile(ctx context.Context, args json.RawMessage, root *os.Root) (interface{}, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Path == "" {
		return nil, fmt.Errorf("'path' is required")
	}

	cleanPath, err := resolvePathWithinRoots(params.Path)
	if err != nil {
		return nil, err
	}

	f, err := root.Open(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	if info.IsDir() {
		return nil, fmt.Errorf("%q is a directory, not a file", params.Path)
	}

	if info.Size() > maxInlineFileSize {
		pr, pw := io.Pipe()
		defer pr.Close()

		go func() {
			defer pw.Close()
			select {
			case <-ctx.Done():
				pw.CloseWithError(ctx.Err())
				return
			default:
			}
			_, err := io.CopyN(pw, f, maxInlineFileSize)
			if err != nil {
				pw.CloseWithError(err)
			}
		}()

		limitedReader := io.LimitReader(pr, maxInlineFileSize)
		buf, readErr := io.ReadAll(limitedReader)
		if readErr != nil {
			return nil, fmt.Errorf("read file: %w", readErr)
		}

		return ReadFileResult{
			Path:      cleanPath,
			Content:   string(buf),
			Size:      info.Size(),
			Truncated: true,
		}, nil
	}

	buf := make([]byte, info.Size())
	_, err = io.ReadFull(f, buf)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	return ReadFileResult{
		Path:    cleanPath,
		Content: string(buf),
		Size:    info.Size(),
	}, nil
}

type WriteFileResult struct {
	Path         string `json:"path"`
	BytesWritten int64  `json:"bytes_written"`
}

func handleWriteFile(ctx context.Context, args json.RawMessage, root *os.Root) (interface{}, error) {
	var params struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Path == "" {
		return nil, fmt.Errorf("'path' is required")
	}

	cleanPath, err := resolvePathWithinRoots(params.Path)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(cleanPath)
	if dir != "." {
		if err := root.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create parent directories: %w", err)
		}
	}

	// Use root.Create then write content
	f, err := root.Create(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	n, err := f.WriteString(params.Content)
	if err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	return WriteFileResult{
		Path:         cleanPath,
		BytesWritten: int64(n),
	}, nil
}

type DirEntryResult struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
	Mode  string `json:"mode"`
}

type ListDirResult struct {
	Path    string           `json:"path"`
	Entries []DirEntryResult `json:"entries"`
}

func handleListDir(ctx context.Context, args json.RawMessage, root *os.Root) (interface{}, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	dirPath := params.Path
	if dirPath == "" {
		dirPath = "."
	}

	cleanPath, err := resolvePathWithinRoots(dirPath)
	if err != nil {
		return nil, err
	}

	fsys := root.FS()
	info, err := fs.Stat(fsys, cleanPath)
	if err != nil {
		return nil, fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", dirPath)
	}

	entries, err := fs.ReadDir(fsys, cleanPath)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}

	result := make([]DirEntryResult, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			slog.Warn("failed to get file info", "name", e.Name(), "error", err)
			continue
		}
		result = append(result, DirEntryResult{
			Name:  e.Name(),
			Size:  fi.Size(),
			IsDir: e.IsDir(),
			Mode:  fi.Mode().String(),
		})
	}

	return ListDirResult{
		Path:    cleanPath,
		Entries: result,
	}, nil
}

type FileInfoResult struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	IsDir   bool   `json:"is_dir"`
	Mode    string `json:"mode"`
	ModTime string `json:"mod_time"`
}

func handleFileInfo(ctx context.Context, args json.RawMessage, root *os.Root) (interface{}, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Path == "" {
		return nil, fmt.Errorf("'path' is required")
	}

	cleanPath, err := resolvePathWithinRoots(params.Path)
	if err != nil {
		return nil, err
	}

	f, err := root.Open(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("open path: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}

	return FileInfoResult{
		Path:    cleanPath,
		Name:    info.Name(),
		Size:    info.Size(),
		IsDir:   info.IsDir(),
		Mode:    info.Mode().String(),
		ModTime: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
	}, nil
}

type SearchFilesResult struct {
	Pattern string   `json:"pattern"`
	Files   []string `json:"files"`
}

func handleSearchFiles(ctx context.Context, args json.RawMessage, root *os.Root) (interface{}, error) {
	var params struct {
		Pattern string `json:"pattern"`
		Root    string `json:"root"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Pattern == "" {
		return nil, fmt.Errorf("'pattern' is required")
	}

	searchRoot := params.Root
	if searchRoot == "" {
		searchRoot = "."
	}

	cleanRoot, err := resolvePathWithinRoots(searchRoot)
	if err != nil {
		return nil, err
	}

	// Walk the directory tree using fs.WalkDir on the root
	fsys := root.FS()
	var matches []string

	err = fs.WalkDir(fsys, cleanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		matched, matchErr := filepath.Match(params.Pattern, path)
		if matchErr != nil {
			return nil
		}
		if matched {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("search files: %w", err)
	}

	if matches == nil {
		matches = []string{}
	}

	return SearchFilesResult{
		Pattern: params.Pattern,
		Files:   matches,
	}, nil
}

type ReadMultipleResult struct {
	Files  []ReadFileResult `json:"files"`
	Errors []FileError      `json:"errors,omitempty"`
}

type FileError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

func handleReadMultiple(ctx context.Context, args json.RawMessage, root *os.Root) (interface{}, error) {
	var params struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if len(params.Paths) == 0 {
		return nil, fmt.Errorf("'paths' is required and must be a non-empty array")
	}

	files := make([]ReadFileResult, 0, len(params.Paths))
	var fileErrors []FileError

	for _, p := range params.Paths {
		cleanPath, err := resolvePathWithinRoots(p)
		if err != nil {
			fileErrors = append(fileErrors, FileError{Path: p, Error: err.Error()})
			continue
		}

		f, err := root.Open(cleanPath)
		if err != nil {
			fileErrors = append(fileErrors, FileError{Path: p, Error: fmt.Sprintf("open: %v", err)})
			continue
		}

		info, err := f.Stat()
		if err != nil {
			f.Close()
			fileErrors = append(fileErrors, FileError{Path: p, Error: fmt.Sprintf("stat: %v", err)})
			continue
		}

		if info.IsDir() {
			f.Close()
			fileErrors = append(fileErrors, FileError{Path: p, Error: "is a directory"})
			continue
		}

		if info.Size() > maxInlineFileSize {
			pr, pw := io.Pipe()
			go func() {
				defer pw.Close()
				defer f.Close()
				select {
				case <-ctx.Done():
					pw.CloseWithError(ctx.Err())
					return
				default:
				}
				_, err := io.CopyN(pw, f, maxInlineFileSize)
				if err != nil {
					pw.CloseWithError(err)
				}
			}()

			buf, readErr := io.ReadAll(io.LimitReader(pr, maxInlineFileSize))
			pr.Close()
			if readErr != nil {
				fileErrors = append(fileErrors, FileError{Path: p, Error: fmt.Sprintf("read: %v", readErr)})
				continue
			}

			files = append(files, ReadFileResult{
				Path:      cleanPath,
				Content:   string(buf),
				Size:      info.Size(),
				Truncated: true,
			})
		} else {
			buf := make([]byte, info.Size())
			_, err = io.ReadFull(f, buf)
			f.Close()
			if err != nil {
				fileErrors = append(fileErrors, FileError{Path: p, Error: fmt.Sprintf("read: %v", err)})
				continue
			}

			files = append(files, ReadFileResult{
				Path:    cleanPath,
				Content: string(buf),
				Size:    info.Size(),
			})
		}
	}

	result := ReadMultipleResult{
		Files: files,
	}
	if len(fileErrors) > 0 {
		result.Errors = fileErrors
	}
	return result, nil
}
