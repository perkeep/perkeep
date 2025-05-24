package bluesky

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/importer"
)

type Datastore struct {
	root *importer.Object
	host *importer.Host
}

func (d *Datastore) Get(ctx context.Context, key datastore.Key) ([]byte, error) {
	br, ok := blob.Parse(d.root.Attr(key.String()))
	if !ok {
		return nil, fmt.Errorf("invalid blob ref %q", d.root.Attr(key.String()))
	}

	blb, _, err := d.host.BlobSource().Fetch(ctx, br)
	if err != nil {
		return nil, err
	}
	defer blb.Close()

	return io.ReadAll(blb)
}

func (d *Datastore) Has(ctx context.Context, key datastore.Key) (bool, error) {
	return d.root.Attr(key.String()) != "", nil
}

func (d *Datastore) GetSize(ctx context.Context, key datastore.Key) (int, error) {
	return 0, errors.New("not implemented")
}

func (d *Datastore) Query(ctx context.Context, q query.Query) (query.Results, error) {
	return nil, errors.New("not implemented")
}

func (d *Datastore) Put(ctx context.Context, key datastore.Key, value []byte) error {
	h := blob.NewHash()
	if _, err := h.Write(value); err != nil {
		return err
	}

	br := blob.RefFromHash(h)
	_, err := blobserver.Receive(ctx, d.host.Target(), br, bytes.NewReader(value))
	if err != nil {
		return err
	}

	return d.root.SetAttr(key.String(), br.String())
}

func (d *Datastore) Delete(ctx context.Context, key datastore.Key) error {
	return errors.New("not implemented")
}

func (d *Datastore) Sync(ctx context.Context, prefix datastore.Key) error {
	return errors.New("not implemented")
}

func (d *Datastore) Close() error {
	return nil
}

type batch struct {
	d *Datastore
}

func (d *Datastore) Batch(ctx context.Context) (datastore.Batch, error) {
	return &batch{d: d}, nil
}

func (b *batch) Put(ctx context.Context, key datastore.Key, value []byte) error {
	return b.d.Put(ctx, key, value)
}

func (b *batch) Delete(ctx context.Context, key datastore.Key) error {
	return b.d.Delete(ctx, key)
}

func (b *batch) Commit(ctx context.Context) error {
	return nil
}
