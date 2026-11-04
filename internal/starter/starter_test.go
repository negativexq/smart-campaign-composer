package starter_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/negativexq/smart-campaign-composer/internal/asset"
	"github.com/negativexq/smart-campaign-composer/internal/content"
	"github.com/negativexq/smart-campaign-composer/internal/starter"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
)

func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func load(t *testing.T, pool *pgxpool.Pool, store asset.Store) bool {
	t.Helper()
	loaded, err := starter.Load(context.Background(), pool, store)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return loaded
}

func TestLoadShipsFinishedTemplatesWithTheirImages(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewDatabase(t)
	store := asset.DiskStore{Dir: t.TempDir()}

	if !load(t, pool, store) {
		t.Fatal("Load on a new installation reported nothing added")
	}

	rows, err := pool.Query(ctx, `SELECT name, spec FROM email_designs`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name string
		var spec []byte
		if err := rows.Scan(&name, &spec); err != nil {
			t.Fatal(err)
		}
		var design content.Design
		if err := json.Unmarshal(spec, &design); err != nil {
			t.Fatal(err)
		}
		if problem := design.Check(); problem != "" {
			t.Errorf("%s is not finished: %s", name, problem)
		}
		ids := design.AssetIDs()
		if len(ids) == 0 {
			t.Errorf("%s has no images", name)
		}
		for _, id := range ids {
			var file string
			if err := pool.QueryRow(ctx, `SELECT file_name FROM assets WHERE id = $1`, id).Scan(&file); err != nil {
				t.Errorf("%s uses unknown image %s: %v", name, id, err)
				continue
			}
			opened, err := store.Open(ctx, file)
			if err != nil {
				t.Errorf("%s: image file %s is not stored: %v", name, file, err)
				continue
			}
			if data, err := io.ReadAll(opened); err != nil || len(data) == 0 {
				t.Errorf("%s: image file %s is empty: %v", name, file, err)
			}
			opened.Close() //nolint:errcheck // read only
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != len(starter.Designs()) {
		t.Errorf("templates = %d, want %d", seen, len(starter.Designs()))
	}

	var spec []byte
	if err := pool.QueryRow(ctx, `SELECT spec FROM brand_profile`).Scan(&spec); err != nil {
		t.Fatal(err)
	}
	var brand content.Brand
	if err := json.Unmarshal(spec, &brand); err != nil {
		t.Fatal(err)
	}
	if problem := brand.Check(); problem != "" {
		t.Errorf("brand kit: %s", problem)
	}
	if count(t, pool, `SELECT count(*) FROM assets WHERE id::text = $1`, brand.LogoAssetID) != 1 {
		t.Errorf("brand logo %q is not an image in the library", brand.LogoAssetID)
	}
}

func TestLoadDoesNotBringBackWhatWasDeleted(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewDatabase(t)
	store := asset.DiskStore{Dir: t.TempDir()}
	load(t, pool, store)

	if _, err := pool.Exec(ctx, `DELETE FROM email_designs WHERE name = 'Flash Sale'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE brand_profile SET spec = spec || '{"name":"Changed"}'`); err != nil {
		t.Fatal(err)
	}

	if load(t, pool, store) {
		t.Error("a second Load reported adding content")
	}
	if n := count(t, pool, `SELECT count(*) FROM email_designs WHERE name = 'Flash Sale'`); n != 0 {
		t.Error("a deleted template came back")
	}
	if n := count(t, pool, `SELECT count(*) FROM brand_profile WHERE spec->>'name' = 'Changed'`); n != 1 {
		t.Error("a changed brand kit was overwritten")
	}
}

func TestLoadKeepsWhatAlreadyExists(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewDatabase(t)
	if _, err := pool.Exec(ctx, `INSERT INTO email_designs (name, spec) VALUES ('flash sale', '{"name":"mine"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO brand_profile (spec) VALUES ('{"name":"Mine"}')`); err != nil {
		t.Fatal(err)
	}

	if !load(t, pool, asset.DiskStore{Dir: t.TempDir()}) {
		t.Fatal("Load reported nothing added")
	}

	if n := count(t, pool, `SELECT count(*) FROM email_designs WHERE spec->>'name' = 'mine'`); n != 1 {
		t.Error("a template with the same name was overwritten")
	}
	if n := count(t, pool, `SELECT count(*) FROM brand_profile WHERE spec->>'name' = 'Mine'`); n != 1 {
		t.Error("an existing brand kit was overwritten")
	}
	if got, want := count(t, pool, `SELECT count(*) FROM email_designs`), len(starter.Designs()); got != want {
		t.Errorf("templates = %d, want %d", got, want)
	}
}
