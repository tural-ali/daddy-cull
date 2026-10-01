package catalog

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// fakeMovie describes a synthetic QuickTime file: two chunks of media data,
// one video track, and whatever metadata a test gives it.
type fakeMovie struct {
	media      []byte
	created    uint32
	headerLast bool // the movie header after the media data, as cameras write it
	wide       bool // 64-bit chunk offsets
	trimmed    bool // an edit that starts the track later
	xyz        bool // location in udta, as older iPhones write it
	keys       bool // location in the metadata keys, as newer ones do
	title      string
	encoder    string // ©too, as HandBrake writes it in udta's item list
	xmp        bool   // an XMP packet in udta, as ExifTool writes one into a QuickTime file
	xmpUUID    bool   // an XMP packet in a top-level uuid atom, as ExifTool writes one into an MP4; header last only
}

func atom(kind string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	return append(binary.BigEndian.AppendUint32(nil, uint32(8+len(body))), append([]byte(kind), body...)...)
}

func be32(values ...uint32) []byte {
	var out []byte
	for _, v := range values {
		out = binary.BigEndian.AppendUint32(out, v)
	}
	return out
}

func (f fakeMovie) bytes() []byte {
	ftyp := atom("ftyp", []byte("qt  "), be32(0), []byte("qt  "))
	header := func(start uint32) []byte {
		half := uint32(len(f.media) / 2)
		offsets := atom("stco", be32(0, 2, start, start+half))
		if f.wide {
			offsets = atom("co64", be32(0, 2, 0, start, 0, start+half))
		}
		edit := be32(0, 1, 1000, 0, 0x10000)
		if f.trimmed {
			edit = be32(0, 1, 900, 100, 0x10000)
		}
		stbl := atom("stbl",
			atom("stsd", be32(0, 1), atom("avc1", make([]byte, 16))),
			atom("stts", be32(0, 1, 2, 500)),
			atom("stsz", be32(0, 0, 2, half, uint32(len(f.media))-half)),
			atom("stsc", be32(0, 1, 1, 1, 1)),
			offsets)
		trak := atom("trak",
			atom("tkhd", be32(0, f.created, f.created, 1, 0, 1000), make([]byte, 60)),
			atom("edts", atom("elst", edit)),
			atom("mdia",
				atom("mdhd", be32(0, f.created, f.created, 600, 1000, 0)),
				atom("hdlr", be32(0, 0), []byte("vide"), make([]byte, 12), []byte(f.title)),
				atom("minf", stbl)))
		parts := [][]byte{atom("mvhd", be32(0, f.created, f.created, 600, 1000), make([]byte, 80)), trak}
		udta := [][]byte{atom("\xa9nam", []byte(f.title))}
		if f.xyz {
			udta = [][]byte{atom("\xa9xyz", []byte("+51.5007-000.1246/"))}
		}
		if f.encoder != "" {
			udta = append(udta, atom("meta", be32(0), atom("ilst", atom("\xa9too", atom("data", be32(1, 0), []byte(f.encoder))))))
		}
		if f.xmp {
			udta = append(udta, atom("XMP_", []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/" x:xmptk="Image::ExifTool 13.55"/>`)))
		}
		parts = append(parts, atom("udta", udta...))
		if f.keys {
			parts = append(parts, atom("meta", be32(0), atom("keys", be32(0, 1), atom("mdta", []byte("com.apple.quicktime.location.ISO6709")))))
		}
		return atom("moov", parts...)
	}
	mdat := atom("mdat", f.media)
	if f.headerLast {
		var packet []byte
		if f.xmpUUID {
			packet = atom("uuid", xmpUUID, []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"/>`))
		}
		start := uint32(len(ftyp) + len(packet) + 8)
		return bytes.Join([][]byte{ftyp, packet, mdat, header(start)}, nil)
	}
	size := len(header(0))
	start := uint32(len(ftyp) + size + 8)
	return bytes.Join([][]byte{ftyp, header(start), mdat}, nil)
}

func readFake(t *testing.T, body []byte) (movie, string) {
	t.Helper()
	found, err := readMovie(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := footageHash(context.Background(), bytes.NewReader(body), found)
	if err != nil {
		t.Fatal(err)
	}
	return found, sum
}

// Copies of one clip written differently, with the header before or after the
// media, other dates, other names, 32- or 64-bit offsets and the location
// kept in either place, hold the same footage. A trimmed copy, or one whose
// pictures differ by a byte, does not.
func TestFootageIgnoresWhatOnlyDescribesTheFile(t *testing.T) {
	media := bytes.Repeat([]byte("frame of video "), 64)
	original, sum := readFake(t, fakeMovie{media: media, created: 100, headerLast: true, xyz: true, title: "IMG_0001"}.bytes())
	if !original.located || original.mediaBytes != int64(len(media)) {
		t.Fatalf("original read as %+v", original)
	}
	for name, copy := range map[string]fakeMovie{
		"header first":          {media: media, created: 200, keys: true, title: "IMG_0001 (2)"},
		"64-bit offsets":        {media: media, created: 300, headerLast: true, wide: true},
		"another writer's name": {media: media, created: 100, headerLast: true, title: "Exported by something else"},
	} {
		found, other := readFake(t, copy.bytes())
		if other != sum || found.playback != original.playback {
			t.Errorf("%s: not the same footage", name)
		}
		if found.located != (copy.xyz || copy.keys) {
			t.Errorf("%s: located %v", name, found.located)
		}
	}
	trimmed, other := readFake(t, fakeMovie{media: media, created: 100, headerLast: true, trimmed: true}.bytes())
	if other == sum || trimmed.playback == original.playback {
		t.Error("a trimmed copy matched")
	}
	changed := bytes.Clone(media)
	changed[len(changed)-1] ^= 1
	edited, other := readFake(t, fakeMovie{media: changed, created: 100, headerLast: true}.bytes())
	if other == sum || edited.playback != original.playback {
		t.Error("a copy with other pictures matched, or its header read differently")
	}
}

func TestFootageRefusesWhatItCannotCompare(t *testing.T) {
	media := bytes.Repeat([]byte("frame "), 32)
	good := fakeMovie{media: media, headerLast: true}.bytes()
	twoBlocks := append(bytes.Clone(good), atom("mdat", []byte("more media"))...)
	fragmented := append(bytes.Clone(good), atom("moof", be32(0))...)
	// An offset that points outside the media data.
	stray := bytes.Clone(good)
	at := bytes.Index(stray, []byte("stco")) + 12
	binary.BigEndian.PutUint32(stray[at:], 4)
	for name, body := range map[string][]byte{
		"a photo":            append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{0}, 64)...),
		"two media blocks":   twoBlocks,
		"fragments":          fragmented,
		"an offset astray":   stray,
		"no header":          atom("mdat", media),
		"a truncated header": good[:len(good)-10],
	} {
		if _, err := readMovie(bytes.NewReader(body), int64(len(body))); err == nil {
			t.Errorf("%s: read as a movie", name)
		}
	}
}

// The worker reads every video's header, hashes in full only the videos whose
// headers match another's, and the copies it proves join one group with any
// copy byte-identical to one of them. Every name is a synthetic fixture.
func TestFootageCopiesAreGrouped(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := t.TempDir()
	day := filepath.Join(root, "2023", "2023-09", "2023-09-30")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	media := bytes.Repeat([]byte("the same footage "), 128)
	other := bytes.Repeat([]byte("other footage, same length"), 128)[:len(media)]
	original := fakeMovie{media: media, created: 100, headerLast: true, xyz: true}.bytes()
	files := []struct {
		id   int64
		name string
		body []byte
		kind string
		hash string
	}{
		{1, "IMG_2208.MOV", original, "video", "aa"},
		{2, "IMG_2208 (2).MOV", fakeMovie{media: media, created: 200, xmp: true}.bytes(), "video", ""},
		{3, "Copy of IMG_2208.MOV", original, "video", "aa"},
		{4, "IMG_2209.MOV", fakeMovie{media: other, created: 100, headerLast: true}.bytes(), "video", ""},
		{5, "IMG_2210.MOV", fakeMovie{media: media, created: 100, headerLast: true, trimmed: true}.bytes(), "video", ""},
		{6, "CLIP.AVI", []byte("RIFF....AVI LIST not a quicktime file"), "video", ""},
		{7, "IMG_2211.HEIC", []byte("a photo"), "image", ""},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(day, f.name), f.body, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,?,?,'archive')", f.id, "/archive/2023/2023-09/2023-09-30/"+f.name, f.kind, len(f.body)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.write.ExecContext(ctx, "INSERT INTO asset_days(asset_id,day) VALUES(?,'2023-09-30')", f.id); err != nil {
			t.Fatal(err)
		}
		if f.hash != "" {
			if _, err := s.write.ExecContext(ctx, "INSERT INTO asset_evidence(asset_id,full_hash) VALUES(?,?)", f.id, f.hash); err != nil {
				t.Fatal(err)
			}
		}
	}
	roots := MediaRoots{Archive: root}
	read, hashed, err := s.FillFootage(ctx, roots)
	if err != nil {
		t.Fatal(err)
	}
	// Six videos read; the three copies and IMG_2209, whose header plays its
	// media the same way, hashed; the trimmed clip and the AVI not.
	if read != 6 || hashed != 4 {
		t.Fatalf("read %d, hashed %d", read, hashed)
	}
	var media0 int64
	if err = s.read.QueryRowContext(ctx, "SELECT media_bytes FROM asset_footage WHERE asset_id=6").Scan(&media0); err != nil || media0 != 0 {
		t.Fatalf("the AVI was recorded as %d, %v", media0, err)
	}
	if read, hashed, err = s.FillFootage(ctx, roots); err != nil || read != 0 || hashed != 0 {
		t.Fatalf("a second pass read %d and hashed %d, %v", read, hashed, err)
	}
	// A catalogue from before who wrote each file was read has every header
	// read again for it, and keeps the footage hashes it has.
	if _, err = s.write.ExecContext(ctx, "DELETE FROM asset_writer"); err != nil {
		t.Fatal(err)
	}
	if read, hashed, err = s.FillFootage(ctx, roots); err != nil || read != 6 || hashed != 0 {
		t.Fatalf("reading who wrote them read %d and hashed %d, %v", read, hashed, err)
	}
	groups, err := s.ExactDuplicates(ctx, "09-30", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups: %+v", groups)
	}
	group := groups[0]
	ids := []int64{}
	for _, member := range group.Members {
		ids = append(ids, member.ID)
	}
	if group.Proof != "footage" || len(ids) != 3 || ids[0] != 3 || ids[1] != 2 || ids[2] != 1 || group.Members[1].Located || !group.Members[2].Located ||
		!group.Members[1].Retagged || group.Members[2].Retagged || group.Members[2].Converted {
		t.Fatalf("group: %+v", group)
	}
	largest := int64(max(len(original), len(files[1].body)))
	if group.Size != largest || group.Reclaimable != int64(2*len(original)+len(files[1].body))-largest {
		t.Fatalf("size %d, reclaimable %d", group.Size, group.Reclaimable)
	}
	// Once the copy that differs only in its metadata is in the Bin, the two
	// left are byte-identical, and the group says so.
	if _, err = s.write.ExecContext(ctx, "INSERT INTO file_state(asset_id,state,plan_id) VALUES(2,'trashed','test')"); err != nil {
		t.Fatal(err)
	}
	if groups, err = s.ExactDuplicates(ctx, "09-30", 100); err != nil || len(groups) != 1 || groups[0].Proof != "bytes" || groups[0].Hash != "aa" || len(groups[0].Members) != 2 {
		t.Fatalf("after the Bin: %+v %v", groups, err)
	}
	// A file replaced by another is read again.
	if _, err = s.write.ExecContext(ctx, "UPDATE assets SET size_bytes=size_bytes+1 WHERE id=4"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(day, "IMG_2209.MOV"), append(files[3].body, 0), 0o644); err != nil {
		t.Fatal(err)
	}
	if read, _, err = s.FillFootage(ctx, roots); err != nil || read != 1 {
		t.Fatalf("the replaced file was read %d times, %v", read, err)
	}
}

// A copy HandBrake encoded says so in its encoder tag, and one whose metadata
// a tool rewrote carries an XMP packet, in either place ExifTool writes it.
// The camera's own file has neither, and neither changes the footage.
func TestFootageReadsWhoWroteTheFile(t *testing.T) {
	media := bytes.Repeat([]byte("frame of video "), 64)
	camera, sum := readFake(t, fakeMovie{media: media, headerLast: true, keys: true}.bytes())
	if camera.converted || camera.retagged {
		t.Fatalf("the camera's file read as %+v", camera)
	}
	for name, copy := range map[string]fakeMovie{
		"HandBrake":            {media: media, headerLast: true, encoder: "HandBrake 1.8.0 2024052000"},
		"ExifTool in udta":     {media: media, headerLast: true, keys: true, xmp: true},
		"ExifTool in uuid":     {media: media, headerLast: true, xmpUUID: true},
		"another encoder only": {media: media, headerLast: true, encoder: "Lavf60.16.100"},
	} {
		found, other := readFake(t, copy.bytes())
		if other != sum {
			t.Errorf("%s: not the same footage", name)
		}
		if want := copy.encoder != "" && bytes.HasPrefix([]byte(copy.encoder), []byte("HandBrake")); found.converted != want {
			t.Errorf("%s: converted %v, want %v", name, found.converted, want)
		}
		if want := copy.xmp || copy.xmpUUID; found.retagged != want {
			t.Errorf("%s: retagged %v, want %v", name, found.retagged, want)
		}
	}
}
