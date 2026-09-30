package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An XMP sidecar as Photos' exporter writes one: people as a list, as face
// regions and as keywords, a hierarchy of keywords, a rating as an element and
// a title that is only the file's name. Every name is a synthetic fixture.
const exportedXMP = `<?xml version="1.0" encoding="UTF-8"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about=""
    xmlns:dc="http://purl.org/dc/elements/1.1/"
    xmlns:xmp="http://ns.adobe.com/xap/1.0/"
    xmlns:Iptc4xmpExt="http://iptc.org/std/Iptc4xmpExt/2008-02-29/"
    xmlns:digiKam="http://www.digikam.org/ns/1.0/"
    xmlns:MP="http://ns.microsoft.com/photo/1.2/"
    xmlns:MPRI="http://ns.microsoft.com/photo/1.2/t/RegionInfo#"
    xmlns:MPReg="http://ns.microsoft.com/photo/1.2/t/Region#"
    xmlns:mwg-rs="http://www.metadataworkinggroup.com/schemas/regions/">
   <dc:title><rdf:Alt><rdf:li xml:lang="x-default">IMG_0001</rdf:li></rdf:Alt></dc:title>
   <dc:description><rdf:Alt><rdf:li xml:lang="x-default"></rdf:li></rdf:Alt></dc:description>
   <dc:subject><rdf:Bag><rdf:li>Beach</rdf:li><rdf:li>Test Person A</rdf:li></rdf:Bag></dc:subject>
   <digiKam:TagsList><rdf:Seq><rdf:li>Places/Beach</rdf:li><rdf:li>Holiday</rdf:li></rdf:Seq></digiKam:TagsList>
   <Iptc4xmpExt:PersonInImage><rdf:Bag><rdf:li>Test Person A</rdf:li><rdf:li>Test Person B</rdf:li></rdf:Bag></Iptc4xmpExt:PersonInImage>
   <MP:RegionInfo rdf:parseType="Resource"><MPRI:Regions><rdf:Bag><rdf:li rdf:parseType="Resource">
    <MPReg:PersonDisplayName>test person a</MPReg:PersonDisplayName><MPReg:Rectangle>0.1, 0.1, 0.2, 0.2</MPReg:Rectangle>
   </rdf:li></rdf:Bag></MPRI:Regions></MP:RegionInfo>
   <mwg-rs:Regions rdf:parseType="Resource"><mwg-rs:RegionList><rdf:Bag><rdf:li>
    <rdf:Description mwg-rs:Name="Test Person C" mwg-rs:Type="Face"/>
   </rdf:li></rdf:Bag></mwg-rs:RegionList></mwg-rs:Regions>
   <xmp:Rating>4</xmp:Rating>
  </rdf:Description>
 </rdf:RDF>
</x:xmpmeta>`

// The same, as the metadata reader writes a JSON sidecar for a video.
const exportedJSON = `[{"SourceFile":"IMG_0002.MOV","File:FileName":"IMG_0002.MOV","XMP:Rating":5,"XMP:Title":"Birthday at the lake",
  "XMP:PersonInImage":["Test Person A"],"XMP:Subject":["Test Person A","Lake"],"IPTC:Keywords":"Lake",
  "XMP:RegionInfo":{"RegionList":[{"Area":{"H":0.2,"W":0.2,"X":0.5,"Y":0.5},"Name":"Test Person D","Type":"Face"}]}}]`

// Google Takeout's, whose title is the file's name.
const takeoutJSON = `{"title":"PXL_20230101_101010.jpg","description":"","people":[{"name":"Test Person E"}],"photoTakenTime":{"timestamp":"1672567810"}}`

func TestSidecarsAreReadHoweverTheyAreSpelt(t *testing.T) {
	for _, c := range []struct {
		name string
		got  sidecarContent
		want SidecarFacts
	}{
		// Test Person A is named four ways and counted once; keywords that
		// name a person are the person; the hierarchy counts its last part.
		{"xmp", parseSidecarXMP([]byte(exportedXMP)), SidecarFacts{Files: 1, People: 3, Keywords: 2, Rating: 4}},
		{"exiftool json", parseSidecarJSON([]byte(exportedJSON)), SidecarFacts{Files: 1, People: 2, Keywords: 1, Rating: 5, Captioned: true}},
		{"takeout json", parseSidecarJSON([]byte(takeoutJSON)), SidecarFacts{Files: 1, People: 1}},
		{"not xmp", parseSidecarXMP([]byte("<x:xmpmeta><unclosed")), SidecarFacts{Files: 1}},
		{"not json", parseSidecarJSON([]byte("{")), SidecarFacts{Files: 1}},
	} {
		if got := combineSidecars([]sidecarContent{c.got}); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

// Each copy says what its own sidecars record: the ones the Bin would take
// with it. A sidecar a Live Photo's photo shares with its video is not the
// photo's, a copy with none says so, and a copy whose folder cannot be read
// says nothing rather than none. A sidecar that changes is read again.
func TestCopiesSayWhatTheirOwnSidecarsRecord(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := t.TempDir()
	day := filepath.Join(root, "2022", "2022-06", "2022-06-15")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"IMG_0001.MOV": "video", "IMG_0001.MOV.xmp": exportedXMP,
		"IMG_0002.MOV": "video", "IMG_0002.MOV.json": exportedJSON, "IMG_0002.xmp": exportedXMP,
		"IMG_0001 (2022-06-15).MOV": "video",
		"IMG_0003.HEIC":             "photo", "IMG_0003.MOV": "video", "IMG_0003.xmp": exportedXMP, "IMG_0003.AAE": "<plist/>",
	} {
		if err := os.WriteFile(filepath.Join(day, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	member := func(name string) DuplicateMember {
		return DuplicateMember{Asset: Asset{Path: "/archive/2022/2022-06/2022-06-15/" + name}}
	}
	groups := []DuplicateGroup{
		{Members: []DuplicateMember{member("IMG_0001.MOV"), member("IMG_0001 (2022-06-15).MOV")}},
		{Members: []DuplicateMember{member("IMG_0002.MOV"), member("IMG_0003.HEIC"), {Asset: Asset{Path: "/archive/2022/gone/IMG_0004.MOV"}}}},
	}
	s.addSidecarFacts(ctx, groups)
	if groups[0].Members[0].Sidecars != nil {
		t.Fatal("read without an archive to read from")
	}
	s.ReadSidecarsFrom(MediaRoots{Archive: root})
	s.withSidecarFacts(ctx, groups)
	facts := func(g, m int) SidecarFacts {
		if groups[g].Members[m].Sidecars == nil {
			t.Fatalf("%s: no sidecar facts", groups[g].Members[m].Path)
		}
		return *groups[g].Members[m].Sidecars
	}
	if got := facts(0, 0); got != (SidecarFacts{Files: 1, People: 3, Keywords: 2, Rating: 4}) {
		t.Errorf("the exported copy: %+v", got)
	}
	if got := facts(0, 1); got != (SidecarFacts{}) {
		t.Errorf("the copy with no sidecars: %+v", got)
	}
	// Both of IMG_0002's sidecars are its own, and a person in both is one.
	if got := facts(1, 0); got != (SidecarFacts{Files: 2, People: 4, Keywords: 3, Rating: 5, Captioned: true}) {
		t.Errorf("the copy with two sidecars: %+v", got)
	}
	// Named by the stem, the XMP and the edit list are the video's as much
	// as the photo's, so they stay whichever goes.
	if got := facts(1, 1); got != (SidecarFacts{}) {
		t.Errorf("the Live Photo's photo: %+v", got)
	}
	if groups[1].Members[2].Sidecars != nil {
		t.Error("a copy whose folder is gone was said to have no sidecars")
	}

	// A new rating is read.
	xmp := filepath.Join(day, "IMG_0001.MOV.xmp")
	if err := os.WriteFile(xmp, []byte(strings.Replace(exportedXMP, "<xmp:Rating>4", "<xmp:Rating>5", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(xmp, later, later); err != nil {
		t.Fatal(err)
	}
	groups[0].Members[0].Sidecars = nil
	s.withSidecarFacts(ctx, groups)
	if got := facts(0, 0); got.Rating != 5 {
		t.Errorf("the changed sidecar was not read again: %+v", got)
	}
}

// Out of time, nothing is guessed: copies not read have no facts.
func TestSidecarsNotReadInTimeAreLeftOut(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "IMG_0001.MOV.xmp"), []byte(exportedXMP), 0o644); err != nil {
		t.Fatal(err)
	}
	s.ReadSidecarsFrom(MediaRoots{Archive: root})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	groups := []DuplicateGroup{{Members: []DuplicateMember{{Asset: Asset{Path: "/archive/IMG_0001.MOV"}}}}}
	s.withSidecarFacts(ctx, groups)
	if groups[0].Members[0].Sidecars != nil {
		t.Fatalf("read after the time ran out: %+v", groups[0].Members[0].Sidecars)
	}
}
