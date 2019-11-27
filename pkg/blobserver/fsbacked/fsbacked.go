package fsbacked

import (
	"context"
	"database/sql"
	"io"
	"math"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"
	"go4.org/jsonconfig"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

type Storage struct {
	blobserver.Storage

	root string
	db   *sql.DB

	nested blobserver.Storage
}

func New(ctx context.Context, root, dbConnStr string, nested blobserver.Storage) (*Storage, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, errors.Wrapf(err, "making absolute path from %s", root)
	}

	db, err := sql.Open("sqlite3", dbConnStr)
	if err != nil {
		return nil, errors.Wrapf(err, "opening db at %s", dbConnStr)
	}

	_, err = db.ExecContext(ctx, schema)
	if err != nil {
		return nil, errors.Wrap(err, "creating db schema")
	}

	return &Storage{
		root:   absRoot,
		db:     db,
		nested: nested,
	}, nil
}

func (s *Storage) Fetch(ctx context.Context, ref blob.Ref) (io.ReadCloser, uint32, error) {
	const q = `SELECT path FROM file WHERE ref = $1 LIMIT 1`
	var path string
	err := s.db.QueryRowContext(ctx, q, ref.String()).Scan(&path)
	if err == sql.ErrNoRows {
		return s.nested.Fetch(ctx, ref)
	}
	if err != nil {
		return nil, 0, errors.Wrap(err, "querying db")
	}
	abspath, size, err := s.stat(path)
	if err != nil {
		return nil, 0, errors.Wrapf(err, "getting size of file %s", abspath)
	}
	f, err := os.Open(abspath)
	return f, size, errors.Wrapf(err, "opening file %s", abspath)
}

func (s *Storage) ReceiveBlob(ctx context.Context, ref blob.Ref, r io.Reader) (blob.SizedRef, error) {
	f, ok := r.(*os.File)
	if !ok {
		return s.nested.ReceiveBlob(ctx, ref, r)
	}

	abspath, err := filepath.Abs(f.Name())
	if err != nil {
		return blob.SizedRef{}, errors.Wrapf(err, "getting absolute path of %s", f.Name())
	}

	relpath := s.findRelPath(abspath)
	if relpath == "" {
		// File is outside s's tree.
		return s.nested.ReceiveBlob(ctx, ref, r)
	}

	_, size, err := s.stat(relpath)
	if err != nil {
		return blob.SizedRef{}, errors.Wrapf(err, "statting %s", abspath)
	}

	const q = `INSERT INTO file (ref, path) VALUES ($1, $2) ON CONFLICT DO NOTHING`
	res, err := s.db.ExecContext(ctx, q, ref.String(), relpath)
	if err != nil {
		return blob.SizedRef{}, errors.Wrap(err, "writing to db")
	}
	aff, err := res.RowsAffected()
	if err != nil {
		return blob.SizedRef{}, errors.Wrap(err, "counting affected rows")
	}
	if aff == 0 {
		// Path was already present. Check that it has the right ref.
		const checkQ = `SELECT ref FROM file WHERE path = $1`
		var gotstr string
		err = s.db.QueryRowContext(ctx, checkQ, relpath).Scan(&gotstr)
		if err != nil {
			return blob.SizedRef{}, errors.Wrapf(err, "checking existing ref for %s", relpath)
		}
		got, _ := blob.Parse(gotstr)
		if got != ref {
			return blob.SizedRef{}, blobserver.ErrCorruptBlob
		}
	}
	return blob.SizedRef{Ref: ref, Size: size}, nil
}

func (s *Storage) StatBlobs(ctx context.Context, refs []blob.Ref, fn func(blob.SizedRef) error) error {
	var nested []blob.Ref
	for _, ref := range refs {
		const q = `SELECT path FROM file WHERE ref = $1 LIMIT 1`
		var path string
		err := s.db.QueryRowContext(ctx, q, ref.String()).Scan(&path)
		if err == sql.ErrNoRows {
			nested = append(nested, ref)
			continue
		}
		if err != nil {
			return errors.Wrapf(err, "querying db for %s", ref)
		}
		_, size, err := s.stat(path)
		if err != nil {
			return errors.Wrapf(err, "statting %s", path)
		}
		err = fn(blob.SizedRef{Ref: ref, Size: size})
		if err != nil {
			return err
		}
	}
	if len(nested) > 0 {
		return s.nested.StatBlobs(ctx, nested, fn)
	}
	return nil
}

func (s *Storage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)

	if limit == 0 {
		return nil
	}

	nestedCh := make(chan blob.SizedRef)
	nestedErr := make(chan error, 1)

	nestedCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		defer close(nestedErr)

		nestedErr <- s.nested.EnumerateBlobs(nestedCtx, nestedCh, after, limit)
	}()

	const q = `SELECT ref, path FROM file WHERE ref > $1 ORDER BY ref`
	rows, err := s.db.QueryContext(ctx, q, after)
	if err != nil {
		return errors.Wrap(err, "querying db")
	}
	defer rows.Close()

	var (
		dbloop     = true
		nestedloop = true
		last       blob.Ref
		dbref      *blob.Ref
		path       string
		nestedref  *blob.SizedRef
	)
	for {
		if nestedloop && nestedref == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()

			case err = <-nestedErr:
				if err != nil {
					return errors.Wrap(err, "enumerating blobs from nested storage")
				}

			case ref, ok := <-nestedCh:
				if ok {
					nestedref = &ref
				} else {
					nestedloop = false
				}
			}
		}

		if dbloop && dbref == nil {
			if rows.Next() {
				var refstr string

				err = rows.Scan(&refstr, &path)
				if err != nil {
					return errors.Wrap(err, "scanning db row")
				}
				ref, _ := blob.Parse(refstr)
				dbref = &ref
			} else {
				dbloop = false
				if err = rows.Err(); err != nil {
					return errors.Wrap(err, "reading db rows")
				}
			}
		}

		if nestedref == nil && dbref == nil {
			// Done.
			return nil
		}

		var out *blob.SizedRef

		if nestedref != nil && (dbref == nil || nestedref.Ref.Less(*dbref)) {
			out = nestedref
			nestedref = nil
		} else if dbref != nil && (nestedref == nil || dbref.Less(nestedref.Ref)) {
			_, size, err := s.stat(path)
			if err != nil {
				return errors.Wrapf(err, "statting %s", path)
			}
			out = &blob.SizedRef{
				Ref:  *dbref,
				Size: size,
			}
			dbref = nil
		}

		if out != nil {
			if out.Ref == last || out.Ref.Less(last) {
				continue
			}

			select {
			case <-ctx.Done():
				return ctx.Err()

			case dest <- *out:
				last = out.Ref
				if limit > 0 {
					limit--
					if limit == 0 {
						return nil
					}
				}
			}
		}
	}
}

func (s *Storage) RemoveBlobs(ctx context.Context, refs []blob.Ref) error {
	for _, ref := range refs {
		err := s.removeBlob(ctx, ref)
		if err != nil {
			return err
		}
	}
	return s.nested.RemoveBlobs(ctx, refs)
}

func (s *Storage) removeBlob(ctx context.Context, ref blob.Ref) error {
	const q = `DELETE FROM file WHERE ref = $1`
	_, err := s.db.ExecContext(ctx, q, ref.String())
	return err
}

// ErrTooBig is the error when a file's size will not fit into a uint32.
var ErrTooBig = errors.New("file size is too big")

func (s *Storage) stat(path string) (string, uint32, error) {
	abspath := filepath.Join(s.root, path)
	fi, err := os.Stat(abspath)
	if err != nil {
		return "", 0, errors.Wrapf(err, "statting %s", abspath)
	}
	size := fi.Size()
	if size > math.MaxUint32 {
		return "", 0, ErrTooBig
	}
	return abspath, uint32(size), nil
}

func (s *Storage) findRelPath(path string) string {
	if s.root == path {
		return ""
	}
	var (
		base = filepath.Base(path)
		dir  = filepath.Dir(path)
	)
	if dir == s.root {
		return base
	}
	if dir == base {
		return ""
	}
	if r := s.findRelPath(dir); r != "" {
		return filepath.Join(r, base)
	}
	return ""
}

const schema = `
	CREATE TABLE IF NOT EXISTS file (
		path TEXT NOT NULL PRIMARY KEY,
		ref TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS file_ref ON file (ref);
`

func newFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (blobserver.Storage, error) {
	var (
		root      = conf.RequiredString("root")
		db        = conf.RequiredString("db")
		nestedStr = conf.RequiredString("nested")
	)
	if err := conf.Validate(); err != nil {
		return nil, errors.Wrap(err, "validating config")
	}
	nested, err := ld.GetStorage(nestedStr)
	if err != nil {
		return nil, errors.Wrap(err, "instantiating nested storage")
	}
	return New(context.Background(), root, db, nested)
}

func init() {
	blobserver.RegisterStorageConstructor("fsbacked", blobserver.StorageConstructor(newFromConfig))
}
