package nodeinstall

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractTarGz unpacks the gzip compressed tar stream r into dst.
//
// Only regular files and directories are materialised; symlinks, hardlinks and
// device nodes are skipped because only the node binary is required. Entry
// names are validated against dst so absolute paths and ".." components
// (Zip-Slip) are rejected before anything is written.
func extractTarGz(dst string, r io.Reader) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("nodeinstall: create extraction dir: %w", err)
	}
	root, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("nodeinstall: resolve extraction dir: %w", err)
	}

	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("nodeinstall: open gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("nodeinstall: read tar entry: %w", err)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			target, err := safeJoin(root, hdr.Name)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("nodeinstall: create dir %q: %w", hdr.Name, err)
			}
		case tar.TypeReg:
			target, err := safeJoin(root, hdr.Name)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("nodeinstall: create dir for %q: %w", hdr.Name, err)
			}
			perm := os.FileMode(0o644)
			if hdr.Mode&0o111 != 0 {
				perm = 0o755
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
			if err != nil {
				return fmt.Errorf("nodeinstall: create file %q: %w", hdr.Name, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("nodeinstall: write file %q: %w", hdr.Name, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("nodeinstall: close file %q: %w", hdr.Name, err)
			}
			if base := filepath.Base(target); base == "node" || base == "node.exe" {
				if err := os.Chmod(target, 0o755); err != nil {
					return fmt.Errorf("nodeinstall: chmod %q: %w", hdr.Name, err)
				}
			}
		default:
			// Links and special files are not needed; ignore them.
		}
	}
}

// safeJoin resolves name inside root and fails when the result would escape it.
func safeJoin(root, name string) (string, error) {
	unsafe := func(reason string) error {
		return fmt.Errorf("nodeinstall: unsafe archive entry %q: %s", name, reason)
	}
	if name == "" {
		return "", unsafe("empty name")
	}
	if strings.ContainsRune(name, '\x00') {
		return "", unsafe("contains NUL byte")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) {
		return "", unsafe("absolute path")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", unsafe("path escapes destination")
	}
	target := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", unsafe("cannot resolve path")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", unsafe("path escapes destination")
	}
	return target, nil
}

// extractZip unpacks the zip archive in r into dst, for platforms whose Node.js
// release ships .zip instead of .tar.gz (Windows).
//
// Same rules as extractTarGz: only files and directories are written, entry
// names are validated against dst, and node/node.exe is made executable.
func extractZip(dst string, r io.ReaderAt, size int64) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("nodeinstall: create extraction dir: %w", err)
	}
	root, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("nodeinstall: resolve extraction dir: %w", err)
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return fmt.Errorf("nodeinstall: open zip archive: %w", err)
	}
	for _, f := range zr.File {
		target, err := safeJoin(root, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("nodeinstall: create dir %q: %w", f.Name, err)
			}
			continue
		}
		if !f.Mode().IsRegular() {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("nodeinstall: create dir for %q: %w", f.Name, err)
		}
		perm := os.FileMode(0o644)
		if f.Mode()&0o111 != 0 {
			perm = 0o755
		}
		src, err := f.Open()
		if err != nil {
			return fmt.Errorf("nodeinstall: open %q: %w", f.Name, err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
		if err != nil {
			src.Close()
			return fmt.Errorf("nodeinstall: create file %q: %w", f.Name, err)
		}
		if _, err := io.Copy(out, src); err != nil {
			out.Close()
			src.Close()
			return fmt.Errorf("nodeinstall: write file %q: %w", f.Name, err)
		}
		_ = src.Close()
		if err := out.Close(); err != nil {
			return fmt.Errorf("nodeinstall: close file %q: %w", f.Name, err)
		}
		if base := filepath.Base(target); base == "node" || base == "node.exe" {
			if err := os.Chmod(target, 0o755); err != nil {
				return fmt.Errorf("nodeinstall: chmod %q: %w", f.Name, err)
			}
		}
	}
	return nil
}
