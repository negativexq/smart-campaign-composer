package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/negativexq/smart-campaign-composer/internal/content"
)

var (
	// ErrDesignNotFound means a saved email design does not exist.
	ErrDesignNotFound = errors.New("email design not found")
	// ErrDesignNameTaken means another saved email design has this name.
	ErrDesignNameTaken = errors.New("email design name taken")
)

// DesignError says what is wrong with a design, in words for the marketer.
type DesignError struct{ Message string }

func (e *DesignError) Error() string { return e.Message }

// SavedDesign is a marketer's own email design and how it is used.
type SavedDesign struct {
	content.Design
	Version   string    `json:"version"`
	Campaigns int       `json:"campaigns"`
	UpdatedAt time.Time `json:"updated_at"`
}

const designSelect = `SELECT id::text, spec, updated_at FROM email_designs `

func scanDesign(row pgx.CollectableRow) (SavedDesign, error) {
	var (
		saved   SavedDesign
		id      string
		encoded []byte
	)
	if err := row.Scan(&id, &encoded, &saved.UpdatedAt); err != nil {
		return SavedDesign{}, err
	}
	if err := json.Unmarshal(encoded, &saved.Design); err != nil {
		return SavedDesign{}, fmt.Errorf("decode design: %w", err)
	}
	saved.ID = id
	saved.Version = content.DesignVersion(saved.Design)
	return saved, nil
}

// EmailDesigns lists a marketer's own designs by name, with how many campaigns use each.
func (s *Service) EmailDesigns(ctx context.Context) ([]SavedDesign, error) {
	rows, err := s.db.Query(ctx, designSelect+`ORDER BY lower(name)`)
	if err != nil {
		return nil, fmt.Errorf("list designs: %w", err)
	}
	designs, err := pgx.CollectRows(rows, scanDesign)
	if err != nil {
		return nil, fmt.Errorf("list designs: %w", err)
	}
	counts, err := s.templateCounts(ctx)
	if err != nil {
		return nil, err
	}
	for i := range designs {
		designs[i].Campaigns = counts[content.CustomTemplateName(designs[i].ID)]
	}
	return designs, nil
}

func (s *Service) emailDesign(ctx context.Context, id string) (SavedDesign, error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(id); err != nil {
		return SavedDesign{}, ErrDesignNotFound
	}
	rows, err := s.db.Query(ctx, designSelect+`WHERE id = $1`, parsed)
	if err != nil {
		return SavedDesign{}, fmt.Errorf("read design: %w", err)
	}
	saved, err := pgx.CollectExactlyOneRow(rows, scanDesign)
	if errors.Is(err, pgx.ErrNoRows) {
		return SavedDesign{}, ErrDesignNotFound
	}
	if err != nil {
		return SavedDesign{}, fmt.Errorf("read design: %w", err)
	}
	return saved, nil
}

// designForTemplate is the copy a campaign keeps of the design a template name points to.
func (s *Service) designForTemplate(ctx context.Context, name string) (*content.Design, error) {
	saved, err := s.emailDesign(ctx, content.CustomTemplateID(name))
	if err != nil {
		return nil, err
	}
	return &saved.Design, nil
}

// SaveEmailDesign adds a design, or changes one when id is set. Campaigns already using
// it keep the copy they took.
func (s *Service) SaveEmailDesign(ctx context.Context, id string, design content.Design) (SavedDesign, error) {
	design = design.Clean()
	design.ID = ""
	if problem := design.Check(); problem != "" {
		return SavedDesign{}, &DesignError{problem}
	}
	if err := s.checkAssetsExist(ctx, design.AssetIDs()); err != nil {
		return SavedDesign{}, err
	}
	spec, err := json.Marshal(design)
	if err != nil {
		return SavedDesign{}, fmt.Errorf("encode design: %w", err)
	}
	if id == "" {
		err = s.db.QueryRow(ctx, `INSERT INTO email_designs (name, spec, created_by) VALUES ($1, $2, $3) RETURNING id::text`,
			design.Name, spec, personFrom(ctx)).Scan(&id)
	} else {
		if _, err = s.emailDesign(ctx, id); err != nil {
			return SavedDesign{}, err
		}
		_, err = s.db.Exec(ctx, `UPDATE email_designs SET name = $2, spec = $3, updated_at = now() WHERE id = $1`, id, design.Name, spec)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return SavedDesign{}, ErrDesignNameTaken
	}
	if err != nil {
		return SavedDesign{}, fmt.Errorf("save design: %w", err)
	}
	return s.emailDesign(ctx, id)
}

// DeleteEmailDesign removes a design. Campaigns that used it keep their copy.
func (s *Service) DeleteEmailDesign(ctx context.Context, id string) error {
	if _, err := s.emailDesign(ctx, id); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM email_designs WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete design: %w", err)
	}
	return nil
}
