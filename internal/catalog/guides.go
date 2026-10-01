package catalog

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Each page opens with a short guide until the reviewer hides it. Which
// guides are hidden is kept in the catalogue rather than the browser, so a
// guide hidden once stays hidden at every address Cull is opened at and on
// every device. Each page is a settings row of its own, so two tabs hiding
// two guides at once cannot undo each other.
const guideSettingPrefix = "guide_hidden."

// guidePage is a page's name as the app routes it, such as "today" or
// "upgrades".
var guidePage = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// HiddenGuides lists the pages whose guide is hidden, by name, in order.
func (s *Store) HiddenGuides(ctx context.Context) ([]string, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT key FROM settings WHERE key LIKE ? ESCAPE '\\' ORDER BY key", strings.ReplaceAll(guideSettingPrefix, "_", "\\_")+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pages := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		pages = append(pages, strings.TrimPrefix(key, guideSettingPrefix))
	}
	return pages, rows.Err()
}

// SetGuideHidden hides or shows the guide of one page.
func (s *Store) SetGuideHidden(ctx context.Context, page string, hidden bool) error {
	if !guidePage.MatchString(page) {
		return fmt.Errorf("%w: page %q", ErrInvalid, page)
	}
	var err error
	if hidden {
		_, err = s.write.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES(?,'hidden') ON CONFLICT(key) DO NOTHING", guideSettingPrefix+page)
	} else {
		_, err = s.write.ExecContext(ctx, "DELETE FROM settings WHERE key=?", guideSettingPrefix+page)
	}
	return err
}

// ShowAllGuides shows every page's guide again.
func (s *Store) ShowAllGuides(ctx context.Context) error {
	_, err := s.write.ExecContext(ctx, "DELETE FROM settings WHERE key LIKE ? ESCAPE '\\'", strings.ReplaceAll(guideSettingPrefix, "_", "\\_")+"%")
	return err
}
