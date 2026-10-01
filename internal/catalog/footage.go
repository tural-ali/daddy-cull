package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"os"
	"strconv"
	"time"
)

// Two copies of one video are often not the same file. A clip downloaded from
// iCloud twice, or exported once from Photos and once from the cloud, carries
// the same pictures and sound, byte for byte, but its metadata differs: where
// it was taken, when the file was written, the order of the parts inside it.
// A full hash of the file tells such copies apart, so they never reach the
// Duplicates page, though keeping both only wastes the space.
//
// A QuickTime or MP4 file keeps its pictures and sound in one block, the
// media data, and describes how to play them in another, the movie header.
// Two files whose media data is identical and whose headers play it the same
// way, sample for sample, frame the same way and at the same times, show and
// sound the same. That is what is proven here: the media data is hashed in
// full, and with it every part of the header that decides what is played.
// What only describes the file, such as its dates, location, title or the
// application that wrote it, is left out. A copy trimmed, re-encoded, turned
// or edited plays differently, so it never matches.
//
// This is done in two steps, as sizes and full hashes are for photos: the
// headers, which are small, are read for every video, and only the videos
// whose headers play their media the same way as another's are hashed in
// full.

// footageBatch bounds how many videos one pass hashes in full. They are long
// reads, so a pass stops after these and the next goes on at once.
const footageBatch = 200

// headerBatch bounds how many headers one pass reads.
const headerBatch = 5000

// maxMovieHeader is the largest movie header read. A header this size would
// describe hours of footage; one larger is not read, and the video is not
// compared.
const maxMovieHeader = 64 << 20

// errNotMovie means the file is not a movie whose footage can be compared.
var errNotMovie = errors.New("not a comparable movie")

// movie is what a video's header says about its footage.
type movie struct {
	// mediaStart and mediaBytes place the media data in the file.
	mediaStart, mediaBytes int64
	// playback is the hash of every part of the header that decides what is
	// played, with the chunk offsets made relative to the media data.
	playback string
	// located is true when the file records where it was taken.
	located bool
	// converted is true when HandBrake wrote the file: its pictures were
	// encoded again from another file, the original.
	converted bool
	// retagged is true when the file carries an XMP packet, which a camera
	// never writes into a movie: a tool such as ExifTool, or an export that
	// uses it, rewrote what the file records after it was taken.
	retagged bool
}

// xmpUUID opens the top-level atom an MP4 keeps its XMP packet in.
var xmpUUID = []byte{0xbe, 0x7a, 0xcf, 0xcb, 0x97, 0xa9, 0x42, 0xe8, 0x9c, 0x71, 0x99, 0x94, 0x91, 0xe3, 0xaf, 0xac}

// readMovie reads a QuickTime or MP4 file's layout and header. It fails for
// any other file, and for a movie it cannot compare: one whose media data is
// in more than one block, one split into fragments, or one whose header
// points outside its media data.
func readMovie(file io.ReaderAt, size int64) (movie, error) {
	var found movie
	var header []byte
	media := 0
	var head [16]byte
	for position, atoms := int64(0), 0; position < size; atoms++ {
		if atoms == 1000 {
			return found, errNotMovie
		}
		// A few bytes of padding can trail the last atom.
		if size-position < 8 {
			break
		}
		if _, err := file.ReadAt(head[:8], position); err != nil {
			return found, err
		}
		length, kind, start := int64(binary.BigEndian.Uint32(head[:4])), string(head[4:8]), int64(8)
		if !printableKind(head[4:8]) {
			return found, errNotMovie
		}
		switch length {
		case 1:
			if _, err := file.ReadAt(head[8:16], position+8); err != nil {
				return found, err
			}
			length, start = int64(binary.BigEndian.Uint64(head[8:16])), 16
		case 0:
			length = size - position
		}
		if length < start || length > size-position {
			return found, errNotMovie
		}
		switch kind {
		case "moov":
			if header != nil || length-start > maxMovieHeader {
				return found, errNotMovie
			}
			header = make([]byte, length-start)
			if _, err := file.ReadAt(header, position+start); err != nil {
				return found, err
			}
		case "mdat":
			// An empty block holds nothing, and some writers leave one.
			if length > start {
				media++
				found.mediaStart, found.mediaBytes = position+start, length-start
			}
		case "moof", "mfra":
			return found, errNotMovie
		case "uuid":
			var id [16]byte
			if length-start >= 16 {
				if _, err := file.ReadAt(id[:], position+start); err != nil {
					return found, err
				}
				found.retagged = found.retagged || bytes.Equal(id[:], xmpUUID)
			}
		}
		position += length
	}
	if header == nil || media != 1 {
		return found, errNotMovie
	}
	return found, found.readHeader(header)
}

func printableKind(kind []byte) bool {
	for _, c := range kind {
		// © opens Apple's own names, as in ©xyz.
		if (c < 0x20 || c > 0x7e) && c != 0xa9 {
			return false
		}
	}
	return true
}

// Containers whose children are walked. The others, udta and meta among
// them, only describe the file, so what they hold is never hashed.
var movieContainers = map[string]bool{"trak": true, "mdia": true, "minf": true, "stbl": true, "edts": true}

// The parts of a track that decide what is played and how: the samples, their
// sizes, times and order, the codec's own settings, and the edits that trim
// or delay them.
var playbackParts = map[string]bool{"stsd": true, "stts": true, "ctts": true, "cslg": true, "stss": true, "stps": true, "sdtp": true, "stsz": true, "stz2": true, "stsc": true, "elst": true}

func (m *movie) readHeader(header []byte) error {
	digest := sha256.New()
	record := func(path string, body []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(path)))
		digest.Write(n[:])
		digest.Write([]byte(path))
		binary.BigEndian.PutUint64(n[:], uint64(len(body)))
		digest.Write(n[:])
		digest.Write(body)
	}
	tracks := 0
	var walk func(body []byte, path string) error
	walk = func(body []byte, path string) error {
		for len(body) > 0 {
			kind, inner, rest, err := nextAtom(body)
			if err != nil {
				return err
			}
			body = rest
			name := path + "/" + kind
			switch {
			case kind == "trak" && path == "":
				name = "/trak" + strconv.Itoa(tracks)
				tracks++
				if err = walk(inner, name); err != nil {
					return err
				}
			case movieContainers[kind]:
				if err = walk(inner, name); err != nil {
					return err
				}
			case kind == "mvhd" || kind == "tkhd" || kind == "mdhd":
				// When the file was written is not what it plays.
				timeless, ok := withoutDates(inner)
				if !ok {
					return errNotMovie
				}
				record(name, timeless)
			case kind == "hdlr" && len(inner) >= 12:
				// The kind of track, not the name the writer gave it.
				record(name, inner[8:12])
			case playbackParts[kind]:
				record(name, inner)
			case kind == "stco" || kind == "co64":
				offsets, ok := m.relativeOffsets(inner, kind == "co64")
				if !ok {
					return errNotMovie
				}
				// Either table places the same chunks, so both are hashed as one.
				record(path+"/chunks", offsets)
			case kind == "udta" && path == "":
				m.located = m.located || hasAtom(inner, "\xa9xyz")
				m.retagged = m.retagged || hasAtom(inner, "XMP_")
				m.converted = m.converted || handBrake(inner)
			case kind == "meta" && path == "":
				m.located = m.located || bytes.Contains(inner, []byte("com.apple.quicktime.location.ISO6709"))
				m.converted = m.converted || handBrake(inner)
			}
		}
		return nil
	}
	if err := walk(header, ""); err != nil {
		return err
	}
	if tracks == 0 {
		return errNotMovie
	}
	m.playback = hex.EncodeToString(digest.Sum(nil))
	return nil
}

// nextAtom splits the first atom off a header's body.
func nextAtom(body []byte) (kind string, inner, rest []byte, err error) {
	if len(body) < 8 {
		// Some writers pad a container with four zero bytes.
		if len(body) == 4 && binary.BigEndian.Uint32(body) == 0 {
			return "", nil, nil, nil
		}
		return "", nil, nil, errNotMovie
	}
	length, start := uint64(binary.BigEndian.Uint32(body[:4])), uint64(8)
	if length == 1 {
		if len(body) < 16 {
			return "", nil, nil, errNotMovie
		}
		length, start = binary.BigEndian.Uint64(body[8:16]), 16
	} else if length == 0 {
		length = uint64(len(body))
	}
	if length < start || length > uint64(len(body)) {
		return "", nil, nil, errNotMovie
	}
	return string(body[4:8]), body[start:length], body[length:], nil
}

// hasAtom reports whether a body holds an atom of this kind at its top level.
func hasAtom(body []byte, want string) bool {
	for len(body) > 0 {
		kind, inner, rest, err := nextAtom(body)
		if err != nil {
			return false
		}
		if kind == want && len(inner) > 0 {
			return true
		}
		body = rest
	}
	return false
}

// handBrake reports whether metadata names HandBrake as the file's encoder,
// in the ©too atom QuickTime and MP4 keep it in, directly or in a list.
func handBrake(body []byte) bool {
	at := bytes.Index(body, []byte("\xa9too"))
	return at >= 0 && bytes.Contains(body[at:min(len(body), at+64)], []byte("HandBrake"))
}

// withoutDates drops the creation and modification times from a movie, track
// or media header, which open each of them after the version and flags.
func withoutDates(body []byte) ([]byte, bool) {
	if len(body) < 4 {
		return nil, false
	}
	width := 4
	if body[0] == 1 {
		width = 8
	}
	if len(body) < 4+2*width {
		return nil, false
	}
	return append(append([]byte{}, body[:4]...), body[4+2*width:]...), true
}

// relativeOffsets reads a chunk offset table and returns its offsets measured
// from the start of the media data, as eight bytes each. ok is false when an
// offset falls outside the media data.
func (m *movie) relativeOffsets(body []byte, wide bool) ([]byte, bool) {
	if len(body) < 8 {
		return nil, false
	}
	count := uint64(binary.BigEndian.Uint32(body[4:8]))
	width := uint64(4)
	if wide {
		width = 8
	}
	if uint64(len(body)-8) < count*width {
		return nil, false
	}
	out := make([]byte, 0, 8*count)
	for i := range count {
		at := body[8+i*width:]
		var offset uint64
		if wide {
			offset = binary.BigEndian.Uint64(at)
		} else {
			offset = uint64(binary.BigEndian.Uint32(at))
		}
		if offset < uint64(m.mediaStart) || offset >= uint64(m.mediaStart+m.mediaBytes) {
			return nil, false
		}
		out = binary.BigEndian.AppendUint64(out, offset-uint64(m.mediaStart))
	}
	return out, true
}

// footageHash hashes a movie's media data with the header's playback hash, so
// equal hashes mean the same footage played the same way.
func footageHash(ctx context.Context, file io.ReaderAt, found movie) (string, error) {
	digest := sha256.New()
	digest.Write([]byte(found.playback))
	if _, err := io.Copy(digest, &contextReader{ctx, io.NewSectionReader(file, found.mediaStart, found.mediaBytes)}); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// FillFootage reads the headers of live videos not read at their current
// size, then hashes in full the ones whose headers play their media the same
// way as another's, and returns how many headers and hashes it recorded. A
// file that cannot be reached is left for a later pass; one that is not a
// comparable movie is recorded as such, so it is not read again until it
// changes.
func (s *Store) FillFootage(ctx context.Context, roots MediaRoots) (read, hashed int, err error) {
	read, err = s.readHeaders(ctx, roots)
	if err != nil {
		return read, 0, err
	}
	hashed, err = s.hashFootage(ctx, roots)
	return read, hashed, err
}

func (s *Store) readHeaders(ctx context.Context, roots MediaRoots) (int, error) {
	targets, err := s.footageTargets(ctx, `SELECT a.id,a.relative_path,a.size_bytes FROM assets a
	  LEFT JOIN asset_footage f ON f.asset_id=a.id
	  LEFT JOIN asset_writer wr ON wr.asset_id=a.id
	 WHERE a.kind='video' AND a.size_bytes>0
	   AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
	   AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)
	   AND a.id NOT IN (`+liveClipAssets+`)
	   AND (f.asset_id IS NULL OR f.size_bytes!=a.size_bytes OR wr.asset_id IS NULL OR wr.size_bytes!=a.size_bytes)
	 ORDER BY a.id
	 LIMIT ?`, headerBatch)
	if err != nil {
		return 0, err
	}
	read := 0
	for _, target := range targets {
		var found movie
		var mtime int64
		opened := withCatalogued(roots, target, func(file *os.File, info os.FileInfo) bool {
			mtime = info.ModTime().Unix()
			found, err = readMovie(file, info.Size())
			if errors.Is(err, errNotMovie) {
				found, err = movie{}, nil
			}
			return err == nil
		})
		if ctx.Err() != nil {
			return read, ctx.Err()
		}
		if !opened {
			continue
		}
		var playback any
		if found.playback != "" {
			playback = found.playback
		}
		// A file read again as it was, at the same size and time with the
		// same header, keeps the footage hash it has.
		if _, err = s.write.ExecContext(ctx, `INSERT INTO asset_footage(asset_id,size_bytes,mtime,media_bytes,playback_hash,footage_hash,located,read_at) VALUES(?,?,?,?,?,NULL,?,?)
			ON CONFLICT(asset_id) DO UPDATE SET footage_hash=CASE WHEN asset_footage.size_bytes=excluded.size_bytes AND asset_footage.mtime=excluded.mtime AND asset_footage.media_bytes=excluded.media_bytes AND asset_footage.playback_hash IS excluded.playback_hash THEN asset_footage.footage_hash END,
			  size_bytes=excluded.size_bytes,mtime=excluded.mtime,media_bytes=excluded.media_bytes,playback_hash=excluded.playback_hash,located=excluded.located,read_at=excluded.read_at`,
			target.id, target.size, mtime, found.mediaBytes, playback, found.located, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return read, err
		}
		if _, err = s.write.ExecContext(ctx, `INSERT INTO asset_writer(asset_id,size_bytes,converted,retagged) VALUES(?,?,?,?)
			ON CONFLICT(asset_id) DO UPDATE SET size_bytes=excluded.size_bytes,converted=excluded.converted,retagged=excluded.retagged`,
			target.id, target.size, found.converted, found.retagged); err != nil {
			return read, err
		}
		read++
	}
	return read, nil
}

// footageTwins is the population worth hashing in full: live videos read at
// their current size whose media data is the same length as another's and
// whose header plays it the same way.
const footageTwins = `twins AS (
	SELECT f.media_bytes,f.playback_hash FROM asset_footage f JOIN assets a ON a.id=f.asset_id AND a.size_bytes=f.size_bytes
	 WHERE f.playback_hash IS NOT NULL
	   AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
	   AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)
	   AND a.id NOT IN (` + liveClipAssets + `)
	 GROUP BY f.media_bytes,f.playback_hash HAVING count(*)>1
)`

func (s *Store) hashFootage(ctx context.Context, roots MediaRoots) (int, error) {
	targets, err := s.footageTargets(ctx, `WITH `+footageTwins+`
	SELECT a.id,a.relative_path,a.size_bytes FROM asset_footage f
	  JOIN assets a ON a.id=f.asset_id AND a.size_bytes=f.size_bytes
	  JOIN twins t ON t.media_bytes=f.media_bytes AND t.playback_hash=f.playback_hash
	 WHERE f.footage_hash IS NULL
	   AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
	   AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)
	   AND a.id NOT IN (`+liveClipAssets+`)
	 ORDER BY f.media_bytes,f.playback_hash,a.id
	 LIMIT ?`, footageBatch)
	if err != nil {
		return 0, err
	}
	hashed := 0
	for _, target := range targets {
		var found movie
		var sum string
		var mtime int64
		ok := withCatalogued(roots, target, func(file *os.File, info os.FileInfo) bool {
			mtime = info.ModTime().Unix()
			// The header is read again, so the hash is of the file as it is
			// now, and the header it is recorded with is the one it was taken
			// with.
			if found, err = readMovie(file, info.Size()); err != nil {
				return false
			}
			sum, err = footageHash(ctx, file, found)
			return err == nil
		})
		if ctx.Err() != nil {
			return hashed, ctx.Err()
		}
		if !ok {
			continue
		}
		if _, err = s.write.ExecContext(ctx, `UPDATE asset_footage SET mtime=?,media_bytes=?,playback_hash=?,footage_hash=?,located=?,read_at=? WHERE asset_id=? AND size_bytes=?`,
			mtime, found.mediaBytes, found.playback, sum, found.located, time.Now().UTC().Format(time.RFC3339), target.id, target.size); err != nil {
			return hashed, err
		}
		hashed++
	}
	return hashed, nil
}

func (s *Store) footageTargets(ctx context.Context, query string, limit int) ([]shapeTarget, error) {
	rows, err := s.read.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []shapeTarget
	for rows.Next() {
		var target shapeTarget
		if err = rows.Scan(&target.id, &target.relative, &target.size); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

// withCatalogued opens a catalogued file through its read-only mount, as the
// media handler opens it, and hands it to use. It reports false when the file
// could not be opened, was not the size catalogued, changed while use read
// it, or use reported false.
func withCatalogued(roots MediaRoots, target shapeTarget, use func(*os.File, os.FileInfo) bool) bool {
	mountRoot, inMount, known := roots.root(target.relative, target.id)
	if !known {
		return false
	}
	root, err := os.OpenRoot(mountRoot)
	if err != nil {
		return false
	}
	defer root.Close()
	file, err := root.Open(inMount)
	if err != nil {
		return false
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != target.size {
		return false
	}
	if !use(file, before) {
		return false
	}
	after, err := file.Stat()
	return err == nil && after.Size() == before.Size() && after.ModTime().Equal(before.ModTime())
}

// keepFootage runs one pass of FillFootage and logs what it did.
func (s *Store) keepFootage(ctx context.Context, roots MediaRoots) (more bool) {
	started := time.Now()
	read, hashed, err := s.FillFootage(ctx, roots)
	if err != nil && ctx.Err() == nil {
		log.Printf("video footage: %v", err)
	} else if read > 0 || hashed > 0 {
		log.Printf("video footage: read %d headers and hashed %d videos in %s", read, hashed, time.Since(started).Round(time.Second))
	}
	return read == headerBatch || hashed == footageBatch
}
