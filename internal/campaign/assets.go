package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/negativexq/smart-campaign-composer/internal/asset"
	"github.com/negativexq/smart-campaign-composer/internal/content"
)

var (
	// ErrAssetNotFound means an image does not exist.
	ErrAssetNotFound = errors.New("asset not found")
	// ErrAssetInUse means a template, the brand kit or a campaign shows this image.
	ErrAssetInUse = errors.New("asset in use")
)

// Asset is an uploaded image. Its file never changes, so what shows it stays as it was.
type Asset struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MIME      string    `json:"mime"`
	Bytes     int       `json:"bytes"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	InUse     bool      `json:"in_use"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
	fileName  string
}

// FileName is where the store keeps this image.
func (a Asset) FileName() string { return a.fileName }

// A campaign's words live in its versions, and each carries the design it was written with.
const assetUsed = `(
	EXISTS (SELECT 1 FROM email_designs WHERE spec::text LIKE '%' || a.id::text || '%')
	OR EXISTS (SELECT 1 FROM brand_profile WHERE spec::text LIKE '%' || a.id::text || '%')
	OR EXISTS (SELECT 1 FROM campaigns WHERE email_design::text LIKE '%' || a.id::text || '%')
	OR EXISTS (SELECT 1 FROM campaign_versions WHERE content::text LIKE '%' || a.id::text || '%')
)`

const assetSelect = `SELECT a.id::text, a.name, a.mime, a.bytes, a.width, a.height, a.file_name, a.created_at, ` + assetUsed + ` FROM assets a `

func scanAsset(row pgx.CollectableRow) (Asset, error) {
	var found Asset
	err := row.Scan(&found.ID, &found.Name, &found.MIME, &found.Bytes, &found.Width, &found.Height, &found.fileName, &found.CreatedAt, &found.InUse)
	found.URL = content.AssetPath(found.ID)
	return found, err
}

// Assets lists the uploaded images, newest first.
func (s *Service) Assets(ctx context.Context) ([]Asset, error) {
	rows, err := s.db.Query(ctx, assetSelect+`ORDER BY a.created_at DESC, a.id`)
	if err != nil {
		return nil, fmt.Errorf("list assets: %w", err)
	}
	found, err := pgx.CollectRows(rows, scanAsset)
	if err != nil {
		return nil, fmt.Errorf("list assets: %w", err)
	}
	return found, nil
}

// AssetByID reads one image's details.
func (s *Service) AssetByID(ctx context.Context, id string) (Asset, error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(id); err != nil {
		return Asset{}, ErrAssetNotFound
	}
	rows, err := s.db.Query(ctx, assetSelect+`WHERE a.id = $1`, parsed)
	if err != nil {
		return Asset{}, fmt.Errorf("read asset: %w", err)
	}
	found, err := pgx.CollectExactlyOneRow(rows, scanAsset)
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrAssetNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("read asset: %w", err)
	}
	return found, nil
}

// RecordAsset remembers a stored image. The same picture uploaded twice is one asset.
func (s *Service) RecordAsset(ctx context.Context, name string, image asset.Image) (Asset, error) {
	var id string
	err := s.db.QueryRow(ctx, `
		INSERT INTO assets (name, mime, bytes, width, height, file_name, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (file_name) DO UPDATE SET file_name = EXCLUDED.file_name
		RETURNING id::text`, name, image.MIME, len(image.Data), image.Width, image.Height, image.FileName(), personFrom(ctx)).Scan(&id)
	if err != nil {
		return Asset{}, fmt.Errorf("record asset: %w", err)
	}
	return s.AssetByID(ctx, id)
}

// DeleteAsset forgets an image nothing shows, and says which file is no longer needed.
func (s *Service) DeleteAsset(ctx context.Context, id string) (string, error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(id); err != nil {
		return "", ErrAssetNotFound
	}
	var fileName string
	err := s.db.QueryRow(ctx, `DELETE FROM assets a WHERE a.id = $1 AND NOT `+assetUsed+` RETURNING a.file_name`, parsed).Scan(&fileName)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := s.AssetByID(ctx, id); err != nil {
			return "", err
		}
		return "", ErrAssetInUse
	}
	if err != nil {
		return "", fmt.Errorf("delete asset: %w", err)
	}
	return fileName, nil
}

func (s *Service) checkAssetsExist(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	var known int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM assets WHERE id::text = ANY($1)`, ids).Scan(&known); err != nil {
		return fmt.Errorf("check assets: %w", err)
	}
	unique := map[string]bool{}
	for _, id := range ids {
		unique[id] = true
	}
	if known != len(unique) {
		return &DesignError{"An image in this template no longer exists. Choose another one."}
	}
	return nil
}

// Brand reads the brand kit, or the starting defaults when nobody has set one.
func (s *Service) Brand(ctx context.Context) (content.Brand, error) {
	var encoded []byte
	err := s.db.QueryRow(ctx, `SELECT spec FROM brand_profile`).Scan(&encoded)
	if errors.Is(err, pgx.ErrNoRows) {
		return content.DefaultBrand(), nil
	}
	if err != nil {
		return content.Brand{}, fmt.Errorf("read brand: %w", err)
	}
	var brand content.Brand
	if err := json.Unmarshal(encoded, &brand); err != nil {
		return content.Brand{}, fmt.Errorf("decode brand: %w", err)
	}
	return brand, nil
}

// SaveBrand changes the brand kit. New templates start from it; existing ones keep their look.
func (s *Service) SaveBrand(ctx context.Context, brand content.Brand) (content.Brand, error) {
	brand = brand.Clean()
	if problem := brand.Check(); problem != "" {
		return content.Brand{}, &DesignError{problem}
	}
	if brand.LogoAssetID != "" {
		if err := s.checkAssetsExist(ctx, []string{brand.LogoAssetID}); err != nil {
			return content.Brand{}, err
		}
	}
	spec, err := json.Marshal(brand)
	if err != nil {
		return content.Brand{}, fmt.Errorf("encode brand: %w", err)
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO brand_profile (spec) VALUES ($1)
		ON CONFLICT (only_row) DO UPDATE SET spec = EXCLUDED.spec, updated_at = now()`, spec); err != nil {
		return content.Brand{}, fmt.Errorf("save brand: %w", err)
	}
	return brand, nil
}
