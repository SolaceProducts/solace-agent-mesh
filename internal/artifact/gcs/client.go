// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//nolint:revive // exported methods on unexported types are interface implementations
package gcs

import (
	"context"

	"cloud.google.com/go/storage"
)

type bucketHandle struct {
	bh *storage.BucketHandle
}

func WrapBucket(bh *storage.BucketHandle) BucketAPI {
	return &bucketHandle{bh: bh}
}

func (b *bucketHandle) Object(name string) ObjectAPI {
	return &objectHandle{oh: b.bh.Object(name)}
}

func (b *bucketHandle) Objects(ctx context.Context, query *storage.Query) ObjectIteratorAPI {
	return b.bh.Objects(ctx, query)
}

type objectHandle struct {
	oh *storage.ObjectHandle
}

func (o *objectHandle) NewWriter(ctx context.Context) WriterAPI {
	return &writerHandle{w: o.oh.NewWriter(ctx)}
}

func (o *objectHandle) NewWriterIfNotExists(ctx context.Context) WriterAPI {
	condObj := o.oh.If(storage.Conditions{DoesNotExist: true})
	return &writerHandle{w: condObj.NewWriter(ctx)}
}

func (o *objectHandle) NewReader(ctx context.Context) (ReaderAPI, error) {
	r, err := o.oh.NewReader(ctx)
	if err != nil {
		return nil, err
	}
	return &readerHandle{r: r}, nil
}

func (o *objectHandle) Attrs(ctx context.Context) (*storage.ObjectAttrs, error) {
	return o.oh.Attrs(ctx)
}

func (o *objectHandle) Delete(ctx context.Context) error {
	return o.oh.Delete(ctx)
}

func (o *objectHandle) CopierTo(dst ObjectAPI) CopierAPI {
	dstObj := dst.(*objectHandle) //nolint:errcheck // only used with real GCS objects
	return &copierHandle{c: dstObj.oh.CopierFrom(o.oh)}
}

type writerHandle struct {
	w *storage.Writer
}

func (wh *writerHandle) Write(p []byte) (int, error) { return wh.w.Write(p) }
func (wh *writerHandle) Close() error                { return wh.w.Close() }
func (wh *writerHandle) SetContentType(ct string)    { wh.w.ContentType = ct }

func (wh *writerHandle) SetMetadata(meta map[string]string) {
	wh.w.Metadata = meta
}

type readerHandle struct {
	r *storage.Reader
}

func (rh *readerHandle) Read(p []byte) (int, error) { return rh.r.Read(p) }
func (rh *readerHandle) Close() error               { return rh.r.Close() }
func (rh *readerHandle) ContentType() string        { return rh.r.Attrs.ContentType }
func (rh *readerHandle) Size() int64                { return rh.r.Attrs.Size }

type copierHandle struct {
	c *storage.Copier
}

func (ch *copierHandle) SetContentType(ct string)           { ch.c.ContentType = ct }
func (ch *copierHandle) SetMetadata(meta map[string]string) { ch.c.Metadata = meta }

func (ch *copierHandle) Run(ctx context.Context) (*storage.ObjectAttrs, error) {
	return ch.c.Run(ctx)
}
