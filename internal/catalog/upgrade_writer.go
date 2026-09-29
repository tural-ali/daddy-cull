package catalog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

// UpgradePlan is a plan for adding a higher-resolution copy from Google
// Takeout to the archive, beside the photo it improves on. Neither the
// archive photo nor the Takeout copy is moved or deleted.
type UpgradePlan struct {
	// ID is the plan's id, 32 hexadecimal characters.
	ID string `json:"id"`
	// ArchiveAssetID is the id in the catalogue of the archive photo the
	// copy improves on.
	ArchiveAssetID int64 `json:"archiveAssetId"`
	// SourceAssetID is the id in the catalogue of the Takeout copy.
	SourceAssetID int64 `json:"sourceAssetId"`
	// Source is where the Takeout copy is, relative to the upgrades folder,
	// without /upgrades/ in front.
	Source string `json:"source"`
	// Destination is where the copy goes, relative to the archive root: in
	// the archive photo's folder, named after it with (hi-res) added, such as
	// 2019/2019-08/2019-08-14/IMG_1234 (hi-res).jpg, and a number after that
	// if the name is taken.
	Destination string `json:"destination"`
	// Size is the Takeout copy's size in bytes.
	Size int64 `json:"size"`
	// Hash is the SHA-256 of the Takeout copy's contents, in hexadecimal.
	// The copy is checked against it before and after copying.
	Hash string `json:"hash"`
	// State is where the plan stands: planned (nothing copied yet), copying,
	// copied, or accepted (the copy is in the archive and recorded).
	State string `json:"state"`
	// Created is when the plan was made, in RFC 3339 UTC.
	Created string `json:"created"`
	// Error says why the last step failed. It is cleared when the plan
	// finishes, and left out when there is none.
	Error string `json:"error,omitempty"`
}

type UpgradeWriter struct {
	s          *Store
	upgrades   *os.Root
	archive    *os.Root
	mu         sync.Mutex
	checkpoint func(string) error
}

func NewUpgradeWriter(s *Store, upgradesRoot, archiveRoot string) (*UpgradeWriter, error) {
	upgrades, err := openGuardedRoot(upgradesRoot)
	if err != nil {
		return nil, err
	}
	archive, err := openGuardedRoot(archiveRoot)
	if err != nil {
		upgrades.Close()
		return nil, err
	}
	return &UpgradeWriter{s: s, upgrades: upgrades, archive: archive}, nil
}

func (w *UpgradeWriter) Close() error {
	first := w.upgrades.Close()
	second := w.archive.Close()
	if first != nil {
		return first
	}
	return second
}

func upgradeRelative(value string) (string, bool) {
	if !strings.HasPrefix(value, "/upgrades/") {
		return "", false
	}
	rel := strings.TrimPrefix(value, "/upgrades/")
	return rel, safeRelative(rel)
}

func archiveRelative(value string) (string, bool) {
	if !strings.HasPrefix(value, "/archive/") {
		return "", false
	}
	rel := strings.TrimPrefix(value, "/archive/")
	return rel, safeRelative(rel)
}

func (w *UpgradeWriter) save(plan *UpgradePlan) error {
	body, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = w.s.write.Exec("INSERT INTO upgrade_plans(id,archive_asset_id,source_asset_id,body) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", plan.ID, plan.ArchiveAssetID, plan.SourceAssetID, string(body))
	return err
}

func (w *UpgradeWriter) load(id string) (*UpgradePlan, error) {
	if len(id) != 32 {
		return nil, ErrInvalid
	}
	if _, err := hex.DecodeString(id); err != nil {
		return nil, ErrInvalid
	}
	var body string
	if err := w.s.read.QueryRow("SELECT body FROM upgrade_plans WHERE id=?", id).Scan(&body); err != nil {
		return nil, err
	}
	var plan UpgradePlan
	if err := json.Unmarshal([]byte(body), &plan); err != nil || plan.ID != id || plan.ArchiveAssetID < 1 || plan.SourceAssetID < 1 || plan.Size < 0 || len(plan.Hash) != 64 {
		return nil, ErrInvalid
	}
	if _, ok := upgradeRelative("/upgrades/" + plan.Source); !ok || !safeRelative(plan.Destination) {
		return nil, ErrInvalid
	}
	return &plan, nil
}

func (w *UpgradeWriter) Preview(ctx context.Context, archiveAssetID, sourceAssetID int64) (*UpgradePlan, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var archivePath, sourcePath, accepted string
	var available bool
	err := w.s.read.QueryRowContext(ctx, `SELECT a.relative_path,s.relative_path,c.source_available,COALESCE(h.accepted_as,'')
		FROM upgrade_candidates c JOIN assets a ON a.id=c.archive_asset_id JOIN assets s ON s.id=c.source_asset_id
		LEFT JOIN upgrade_history h ON h.archive_file=a.relative_path
		WHERE c.archive_asset_id=? AND c.source_asset_id=?`, archiveAssetID, sourceAssetID).Scan(&archivePath, &sourcePath, &available, &accepted)
	if err != nil {
		return nil, err
	}
	if accepted != "" {
		return nil, fmt.Errorf("a higher-resolution copy was already added for this archive file")
	}
	if !available {
		return nil, fmt.Errorf("the staged Google copy is no longer available")
	}
	archiveRel, ok := archiveRelative(archivePath)
	if !ok {
		return nil, ErrInvalid
	}
	sourceRel, ok := upgradeRelative(sourcePath)
	if !ok {
		return nil, ErrInvalid
	}
	if _, err = guardedRegular(w.archive, archiveRel, false); err != nil {
		return nil, fmt.Errorf("archive counterpart is unavailable: %w", err)
	}
	hash, size, err := fingerprintIn(ctx, w.upgrades, sourceRel, false)
	if err != nil {
		return nil, err
	}
	extension := path.Ext(sourceRel)
	stem := strings.TrimSuffix(path.Base(archiveRel), path.Ext(archiveRel))
	destination, err := freeArchiveName(w.archive, path.Join(path.Dir(archiveRel), stem+" (hi-res)"+extension))
	if err != nil {
		return nil, err
	}
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return nil, err
	}
	plan := &UpgradePlan{ID: hex.EncodeToString(random), ArchiveAssetID: archiveAssetID, SourceAssetID: sourceAssetID, Source: sourceRel, Destination: destination, Size: size, Hash: hash, State: "planned", Created: time.Now().UTC().Format(time.RFC3339)}
	if err = w.save(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func (w *UpgradeWriter) verify(ctx context.Context, root *os.Root, rel string, plan *UpgradePlan) error {
	hash, size, err := fingerprintIn(ctx, root, rel, false)
	if err != nil {
		return err
	}
	if hash != plan.Hash || size != plan.Size {
		return fmt.Errorf("upgrade content changed")
	}
	return nil
}

func (w *UpgradeWriter) Run(ctx context.Context, id string) (result *UpgradePlan, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	plan, err := w.load(id)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			plan.Error = err.Error()
			_ = w.save(plan)
		}
		result = plan
	}()
	if plan.State == "accepted" {
		return plan, nil
	}
	if plan.State != "planned" && plan.State != "copying" && plan.State != "copied" {
		return plan, ErrInvalid
	}
	if err = w.verify(ctx, w.upgrades, plan.Source, plan); err != nil {
		return plan, err
	}
	if plan.State == "planned" {
		if _, destinationErr := guardedRegular(w.archive, plan.Destination, false); destinationErr == nil {
			return plan, fmt.Errorf("archive destination became occupied; Google source retained")
		} else if !errors.Is(destinationErr, os.ErrNotExist) {
			return plan, destinationErr
		}
		plan.State = "copying"
		if err = w.save(plan); err != nil {
			return plan, err
		}
	}
	if plan.State == "copying" {
		temp := path.Join(path.Dir(plan.Destination), ".daddy-cull-"+plan.ID+".tmp")
		if _, destinationErr := guardedRegular(w.archive, plan.Destination, false); destinationErr == nil {
			if err = w.verify(ctx, w.archive, plan.Destination, plan); err != nil {
				return plan, fmt.Errorf("archive destination exists with different content")
			}
			plan.State = "copied"
		} else {
			if !errors.Is(destinationErr, os.ErrNotExist) {
				return plan, destinationErr
			}
			if err = mkdirShared(w.archive, path.Dir(plan.Destination)); err != nil {
				return plan, err
			}
			_ = w.archive.Remove(temp)
			source, openErr := w.upgrades.Open(plan.Source)
			if openErr != nil {
				return plan, openErr
			}
			target, createErr := w.archive.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0660)
			if createErr != nil {
				source.Close()
				return plan, createErr
			}
			_, copyErr := io.CopyBuffer(target, source, make([]byte, 1024*1024))
			if copyErr == nil {
				copyErr = target.Sync()
			}
			targetCloseErr := target.Close()
			sourceCloseErr := source.Close()
			if copyErr != nil || targetCloseErr != nil || sourceCloseErr != nil {
				w.archive.Remove(temp)
				if copyErr != nil {
					return plan, copyErr
				}
				if targetCloseErr != nil {
					return plan, targetCloseErr
				}
				return plan, sourceCloseErr
			}
			defer w.archive.Remove(temp)
			if err = w.verify(ctx, w.archive, temp, plan); err != nil {
				return plan, err
			}
			if err = w.archive.Link(temp, plan.Destination); err != nil {
				return plan, fmt.Errorf("archive destination became occupied; Google source retained: %w", err)
			}
			if err = syncRootDir(w.archive, plan.Destination); err != nil {
				return plan, err
			}
			if w.checkpoint != nil {
				if err = w.checkpoint("after-copy"); err != nil {
					return plan, err
				}
			}
			if err = w.verify(ctx, w.archive, plan.Destination, plan); err != nil {
				return plan, err
			}
			plan.State = "copied"
		}
		if err = w.save(plan); err != nil {
			return plan, err
		}
	}
	var archivePath, sourcePath string
	if err = w.s.read.QueryRowContext(ctx, "SELECT relative_path FROM assets WHERE id=? AND source_id='archive'", plan.ArchiveAssetID).Scan(&archivePath); err != nil {
		return plan, err
	}
	if err = w.s.read.QueryRowContext(ctx, "SELECT relative_path FROM assets WHERE id=? AND source_id='takeout'", plan.SourceAssetID).Scan(&sourcePath); err != nil {
		return plan, err
	}
	acceptedAs := "/archive/" + plan.Destination
	tx, err := w.s.write.BeginTx(ctx, nil)
	if err != nil {
		return plan, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO upgrade_history(archive_file,source_file,accepted_as,size_bytes,accepted_at,via)
		VALUES(?,?,?,?,?,?) ON CONFLICT(archive_file) DO UPDATE SET source_file=excluded.source_file,accepted_as=excluded.accepted_as,size_bytes=excluded.size_bytes,accepted_at=excluded.accepted_at,via=excluded.via`, archivePath, sourcePath, acceptedAs, plan.Size, time.Now().UTC().Format(time.RFC3339), "react"); err != nil {
		return plan, err
	}
	plan.State = "accepted"
	plan.Error = ""
	body, _ := json.Marshal(plan)
	if _, err = tx.ExecContext(ctx, "UPDATE upgrade_plans SET body=? WHERE id=?", string(body), plan.ID); err != nil {
		return plan, err
	}
	if err = tx.Commit(); err != nil {
		return plan, err
	}
	return plan, nil
}
