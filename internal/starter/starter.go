// Package starter holds the templates, images and brand kit a new installation opens with,
// so the Template Studio has something real to show and to start from.
package starter

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/negativexq/smart-campaign-composer/internal/asset"
	"github.com/negativexq/smart-campaign-composer/internal/content"
)

//go:embed images/*.png
var imageFiles embed.FS

const (
	author = "setup"
	footer = "Acme Ltd, Istanbul"

	logoFile = "acme-logo.png"

	blockHeading = "heading"
	blockText    = "text"
	blockButton  = "button"
	blockLogo    = "logo"
	blockImage   = "image"
	blockDivider = "divider"

	styleBand    = "band"
	styleBox     = "box"
	styleDisplay = "display"
	styleRule    = "rule"

	alignLeft   = "left"
	alignCenter = "center"
)

// Brand is the brand kit a new installation starts with. LogoAssetID is filled in on load.
func Brand() content.Brand {
	return content.Brand{Name: "Acme", Accent: "#0A7A5C", Page: "#F5F7FA", Button: "rounded", Font: "sans", Footer: footer}
}

// Designs returns the starter templates. Image and logo blocks hold a file name in AssetID
// until Load replaces it with the id that file was given.
func Designs() []content.Design {
	return []content.Design{
		{
			Name: "Flash Sale", Accent: "#D7263D", Page: "#FFF1EC", Button: "pill", Corners: 0, Font: "sans", Footer: footer,
			Blocks: []content.Block{
				logo(alignLeft, 30),
				slot(content.SlotHeadline, blockHeading, alignLeft, styleDisplay),
				slot(content.SlotCTA, blockButton, alignLeft, ""),
				picture("flash-sale.png", "A lightning bolt in a burst of red and yellow"),
				slot(content.SlotBody, blockText, alignLeft, ""),
			},
		},
		{
			Name: "New Arrival", Accent: "#111827", Page: "#F4EFE6", Button: "square", Corners: 0, Font: "sans", Footer: footer,
			Blocks: []content.Block{
				picture("new-arrival.png", "Bold geometric shapes in black, orange, teal and yellow"),
				slot(content.SlotHeadline, blockHeading, alignCenter, styleDisplay),
				slot(content.SlotBody, blockText, alignCenter, ""),
				slot(content.SlotCTA, blockButton, alignCenter, ""),
				logo(alignCenter, 20),
			},
		},
		{
			Name: "Event Invite", Accent: "#0B6E73", Page: "#E6F4F3", Button: "rounded", Corners: 16, Font: "sans", Footer: footer,
			Blocks: []content.Block{
				slot(content.SlotHeadline, blockHeading, alignCenter, styleBand),
				picture("event-stage.png", "Stage lights and confetti over a crowd"),
				slot(content.SlotBody, blockText, alignCenter, ""),
				slot(content.SlotCTA, blockButton, alignCenter, ""),
				logo(alignCenter, 20),
			},
		},
		{
			Name: "Autumn Letter", Accent: "#A84B35", Page: "#FBF3E7", Button: "square", Corners: 8, Font: "serif", Footer: footer,
			Blocks: []content.Block{
				picture("autumn-letter.png", "Rolling hills under a warm evening sun"),
				slot(content.SlotHeadline, blockHeading, alignLeft, styleRule),
				slot(content.SlotBody, blockText, alignLeft, ""),
				slot(content.SlotCTA, blockButton, alignLeft, ""),
				divider(),
				logo(alignLeft, 20),
			},
		},
		{
			Name: "Member Night", Accent: "#26205A", Page: "#ECEAF6", Button: "pill", Corners: 16, Font: "serif", Footer: footer,
			Blocks: []content.Block{
				logo(alignCenter, 30),
				picture("member-night.png", "A golden crescent moon in a starry night sky"),
				slot(content.SlotHeadline, blockHeading, alignCenter, styleBox),
				slot(content.SlotBody, blockText, alignCenter, ""),
				slot(content.SlotCTA, blockButton, alignCenter, ""),
			},
		},
		{
			Name: "Spring Newsletter", Accent: "#2F7D4F", Page: "#EFF7F0", Button: "rounded", Corners: 8, Font: "sans", Footer: footer,
			Blocks: []content.Block{
				logo(alignCenter, 30),
				divider(),
				slot(content.SlotHeadline, blockHeading, alignLeft, styleRule),
				slot(content.SlotBody, blockText, alignLeft, ""),
				picture("spring-fresh.png", "Green leaves and pink flowers on a soft background"),
				slot(content.SlotCTA, blockButton, alignLeft, ""),
			},
		},
		{
			Name: "Winter campaigns", Accent: "#B91C1C", Page: "#F5F7FA", Button: "rounded", Corners: 8, Font: "sans", Footer: footer,
			Blocks: []content.Block{
				logo(alignLeft, 30),
				picture("winter-hero.png", "Snowy mountains and pine trees under falling snow"),
				slot(content.SlotHeadline, blockHeading, alignLeft, ""),
				slot(content.SlotBody, blockText, alignLeft, ""),
				slot(content.SlotCTA, blockButton, alignLeft, ""),
			},
		},
	}
}

func slot(name, kind, align, style string) content.Block {
	return content.Block{ID: "s-" + name, Type: kind, Slot: name, Align: align, Style: style}
}

func logo(align string, width int) content.Block {
	return content.Block{ID: "logo", Type: blockLogo, AssetID: logoFile, Alt: "Acme", Width: width, Align: align}
}

func picture(file, alt string) content.Block {
	return content.Block{ID: "picture", Type: blockImage, AssetID: file, Alt: alt, Width: 100, Align: alignCenter}
}

func divider() content.Block { return content.Block{ID: "line", Type: blockDivider} }

// Load adds the starter content once in an installation's life and reports whether it did.
// Later starts leave everything alone, so a template someone deleted stays deleted. A
// template or brand kit that already exists is never overwritten.
func Load(ctx context.Context, pool *pgxpool.Pool, store asset.Store) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin starter transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// Two setup containers starting together must not both load it.
	if _, err := tx.Exec(ctx, `LOCK TABLE starter_content IN EXCLUSIVE MODE`); err != nil {
		return false, fmt.Errorf("lock starter content: %w", err)
	}
	var loaded bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM starter_content)`).Scan(&loaded); err != nil {
		return false, fmt.Errorf("check starter content: %w", err)
	}
	if loaded {
		return false, nil
	}

	ids, err := putImages(ctx, tx, store)
	if err != nil {
		return false, err
	}
	for _, design := range Designs() {
		if err := insertDesign(ctx, tx, design, ids); err != nil {
			return false, err
		}
	}
	brand := Brand()
	brand.LogoAssetID = ids[logoFile]
	spec, err := json.Marshal(brand)
	if err != nil {
		return false, fmt.Errorf("encode starter brand: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO brand_profile (spec) VALUES ($1) ON CONFLICT (only_row) DO NOTHING`, spec); err != nil {
		return false, fmt.Errorf("insert starter brand: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO starter_content DEFAULT VALUES`); err != nil {
		return false, fmt.Errorf("mark starter content: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit starter content: %w", err)
	}
	return true, nil
}

// putImages stores every image the way an upload would and returns their ids by file name.
func putImages(ctx context.Context, tx pgx.Tx, store asset.Store) (map[string]string, error) {
	entries, err := imageFiles.ReadDir("images")
	if err != nil {
		return nil, fmt.Errorf("list starter images: %w", err)
	}
	ids := make(map[string]string, len(entries))
	for _, entry := range entries {
		raw, err := imageFiles.ReadFile("images/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read starter image %s: %w", entry.Name(), err)
		}
		image, err := asset.Process(raw)
		if err != nil {
			return nil, fmt.Errorf("process starter image %s: %w", entry.Name(), err)
		}
		if err := store.Put(ctx, image.FileName(), image.Data); err != nil {
			return nil, fmt.Errorf("store starter image %s: %w", entry.Name(), err)
		}
		var id string
		err = tx.QueryRow(ctx, `
			INSERT INTO assets (name, mime, bytes, width, height, file_name, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (file_name) DO UPDATE SET file_name = EXCLUDED.file_name
			RETURNING id::text`,
			entry.Name(), image.MIME, len(image.Data), image.Width, image.Height, image.FileName(), author).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("record starter image %s: %w", entry.Name(), err)
		}
		ids[entry.Name()] = id
	}
	return ids, nil
}

func insertDesign(ctx context.Context, tx pgx.Tx, design content.Design, ids map[string]string) error {
	for i, block := range design.Blocks {
		if block.AssetID == "" {
			continue
		}
		id, ok := ids[block.AssetID]
		if !ok {
			return fmt.Errorf("starter template %q uses an image that does not exist: %s", design.Name, block.AssetID)
		}
		design.Blocks[i].AssetID = id
	}
	design = design.Clean()
	if problem := design.Check(); problem != "" {
		return fmt.Errorf("starter template %q: %s", design.Name, problem)
	}
	spec, err := json.Marshal(design)
	if err != nil {
		return fmt.Errorf("encode starter template %q: %w", design.Name, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO email_designs (name, spec, created_by) VALUES ($1, $2, $3)
		ON CONFLICT (lower(name)) DO NOTHING`, design.Name, spec, author); err != nil {
		return fmt.Errorf("insert starter template %q: %w", design.Name, err)
	}
	return nil
}
