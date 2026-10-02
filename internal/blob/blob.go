// Package blob defines the storage boundary used by media uploads. The local
// adapter is for development; a production S3 adapter can satisfy the same API.
package blob

import (
	"context"
	"io"
)

type Object struct {
	Key      string
	MIMEType string
	Size     int64
	Checksum string
}

type Store interface {
	Put(context.Context, string, io.Reader) (Object, error)
	Open(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}
