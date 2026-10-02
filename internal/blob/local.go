// Local stores bounded media files beneath one configured directory. Generated
// keys and filepath.Base prevent request data from selecting filesystem paths.
package blob

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const MaxSize int64 = 10 << 20

var ErrUnsupportedType = errors.New("unsupported media type")
var ErrTooLarge = errors.New("media file exceeds 10 MiB")

type Local struct {
	root string
}

func NewLocal(root string) (*Local, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create blob directory: %w", err)
	}
	return &Local{root: root}, nil
}

func (local *Local) Put(ctx context.Context, key string, source io.Reader) (Object, error) {
	if err := ctx.Err(); err != nil {
		return Object{}, err
	}
	key = filepath.Base(key)
	temporary, err := os.CreateTemp(local.root, ".upload-*")
	if err != nil {
		return Object{}, fmt.Errorf("create temporary blob: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	defer temporary.Close()

	buffered := bufio.NewReader(source)
	header, err := buffered.Peek(512)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return Object{}, fmt.Errorf("inspect media: %w", err)
	}
	mimeType := http.DetectContentType(header)
	extension, ok := extensionForMIME(mimeType)
	if !ok {
		return Object{}, ErrUnsupportedType
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(buffered, MaxSize+1))
	if err != nil {
		return Object{}, fmt.Errorf("write media: %w", err)
	}
	if written > MaxSize {
		return Object{}, ErrTooLarge
	}
	if written == 0 {
		return Object{}, ErrUnsupportedType
	}
	if err := temporary.Sync(); err != nil {
		return Object{}, fmt.Errorf("sync media: %w", err)
	}
	objectKey := key + extension
	if err := os.Rename(temporaryName, filepath.Join(local.root, objectKey)); err != nil {
		return Object{}, fmt.Errorf("commit media: %w", err)
	}
	return Object{Key: objectKey, MIMEType: mimeType, Size: written, Checksum: hex.EncodeToString(hash.Sum(nil))}, nil
}

func (local *Local) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(local.root, filepath.Base(key)))
	if err != nil {
		return nil, fmt.Errorf("open blob: %w", err)
	}
	return file, nil
}

func (local *Local) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(local.root, filepath.Base(key)))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete blob: %w", err)
	}
	return nil
}

func extensionForMIME(mimeType string) (string, bool) {
	extensions := map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/gif":  ".gif",
		"image/webp": ".webp",
		"video/mp4":  ".mp4",
	}
	extension, ok := extensions[mimeType]
	return extension, ok
}
