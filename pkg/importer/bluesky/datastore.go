package bluesky

import (
	"bytes"
	"context"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/importer"
)

func decodeATProtoCID(key datastore.Key) ([]byte, error) {
	cid, _ := strings.CutPrefix(key.String(), "/")

	mh, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(cid)
	if err != nil {
		return nil, err
	}

	if len(mh) != 34 || mh[0] != 0x12 || mh[1] != 0x20 {
		return nil, errors.New("not a sha2-256 multihash")
	}

	digest := mh[2:]

	return digest, nil
}

type Datastore struct {
	root *importer.Object
	host *importer.Host
}

func (d *Datastore) Get(ctx context.Context, key datastore.Key) ([]byte, error) {
	digest, err := decodeATProtoCID(key)
	if err != nil {
		return nil, err
	}

	br, err := blob.FromBytes("sha256", digest)
	if err != nil {
		return nil, fmt.Errorf("invalid blob ref %x", digest)
	}

	blb, _, err := d.host.BlobSource().Fetch(ctx, br)
	if err != nil {
		return nil, err
	}
	defer blb.Close()

	return io.ReadAll(blb)
}

func (d *Datastore) Has(ctx context.Context, key datastore.Key) (bool, error) {
	digest, err := decodeATProtoCID(key)
	if err != nil {
		return false, err
	}

	br, err := blob.FromBytes("sha256", digest)
	if err != nil {
		return false, fmt.Errorf("invalid blob ref %x", digest)
	}

	blb, _, err := d.host.BlobSource().Fetch(ctx, br)
	if err != nil {
		if err == os.ErrNotExist {
			return false, nil
		}
		return false, err
	}
	blb.Close()

	return true, nil
}

func (d *Datastore) GetSize(ctx context.Context, key datastore.Key) (int, error) {
	return 0, errors.New("not implemented")
}

func (d *Datastore) Query(ctx context.Context, q query.Query) (query.Results, error) {
	return nil, errors.New("not implemented")
}

func (d *Datastore) Put(ctx context.Context, key datastore.Key, value []byte) error {
	digest, err := decodeATProtoCID(key)
	if err != nil {
		return err
	}

	h, err := blob.NewHashOfType("sha256")
	if err != nil {
		return err
	}

	if _, err := h.Write(value); err != nil {
		return err
	}

	if bytes.Compare(digest, h.Sum(nil)) != 0 {
		return fmt.Errorf("invalid digest %x", digest)
	}

	br := blob.RefFromHash(h)
	if _, err := blobserver.Receive(ctx, d.host.Target(), br, bytes.NewReader(value)); err != nil {
		return err
	}

	// TODO: where should these refs be attached so they are not "dangling"
	return nil
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
