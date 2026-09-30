package catalog

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The copies of one photo or video are the same pictures, but not always the
// same record of them: the one exported with Photos' sidecars carries who is
// in it, its keywords and its rating, and the one copied off a phone carries
// none. Removing a copy takes its own sidecars to the Bin with it, so the copy
// kept should be the one whose sidecars say the most. The copy groups say, for
// each copy, what its sidecars hold, and the page ranks on that.

// SidecarFacts is what a copy's own sidecars record about it, all of them
// together: the ones that go to the Bin with it, as the Bin finds them.
type SidecarFacts struct {
	// Files is how many sidecars are this copy's own. A sidecar it shares with
	// another file of the same name, such as a Live Photo's video, is not
	// counted: it stays whichever copy goes.
	Files int `json:"files"`
	// People is how many different people are named in it.
	People int `json:"people"`
	// Keywords is how many different keywords it has, besides the people.
	Keywords int `json:"keywords"`
	// Rating is its star rating, 0 when it has none.
	Rating int `json:"rating"`
	// Captioned is whether it has a title or caption that is not just a file
	// name.
	Captioned bool `json:"captioned"`
}

// sidecarContent is what one sidecar file records, kept by name so the
// sidecars of one copy can be counted together without counting a person
// twice.
type sidecarContent struct {
	people    []string
	keywords  []string
	rating    int
	captioned bool
}

// cachedSidecar is a sidecar already read, good while its size and time are
// unchanged.
type cachedSidecar struct {
	size    int64
	mod     time.Time
	content sidecarContent
}

// sidecarReaders is how many folders are read at once. The archive is on
// spinning disks, where more would only queue.
const sidecarReaders = 8

// sidecarBudget is how long a page waits for sidecars before answering
// without the ones not read yet.
const sidecarBudget = 2 * time.Second

// ReadSidecarsFrom lets copy groups say what each copy's sidecars record,
// read from the mounts in roots.
func (s *Store) ReadSidecarsFrom(roots MediaRoots) {
	if roots.Archive != "" {
		s.sidecarRoots.Store(&roots)
	}
}

// WarmSidecarFacts reads the sidecars of every copy the Duplicates page shows
// once, soon after start, so the first page that asks does not wait on the
// disks.
func (s *Store) WarmSidecarFacts(ctx context.Context) {
	if s.sidecarRoots.Load() == nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}
	groups, err := s.copyGroups(ctx, 1000, func(DuplicateMember) bool { return true })
	if err == nil {
		s.addSidecarFacts(ctx, groups)
	}
}

// withSidecarFacts adds what the sidecars record to groups, waiting at most
// sidecarBudget. A copy whose folder is not read by then has none, and the
// page ranks its group without them.
func (s *Store) withSidecarFacts(ctx context.Context, groups []DuplicateGroup) {
	ctx, cancel := context.WithTimeout(ctx, sidecarBudget)
	defer cancel()
	s.addSidecarFacts(ctx, groups)
}

// addSidecarFacts reads the sidecars of every member of groups, a folder at a
// time, since the copies filed together share one listing.
func (s *Store) addSidecarFacts(ctx context.Context, groups []DuplicateGroup) {
	roots := s.sidecarRoots.Load()
	if roots == nil {
		return
	}
	folders := make(map[string][]*DuplicateMember)
	var order []string
	for g := range groups {
		for m := range groups[g].Members {
			member := &groups[g].Members[m]
			dir := path.Dir(member.Path)
			if folders[dir] == nil {
				order = append(order, dir)
			}
			folders[dir] = append(folders[dir], member)
		}
	}
	work := make(chan string)
	var wait sync.WaitGroup
	for range min(sidecarReaders, len(order)) {
		wait.Go(func() {
			for dir := range work {
				s.readFolderSidecars(ctx, *roots, folders[dir])
			}
		})
	}
	for _, dir := range order {
		select {
		case work <- dir:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(work)
	wait.Wait()
}

// readFolderSidecars lists the folder the members share and reads each
// member's own sidecars. Nothing is set for a member if the folder or one of
// its sidecars cannot be read, so what is set is never a guess.
func (s *Store) readFolderSidecars(ctx context.Context, roots MediaRoots, members []*DuplicateMember) {
	// The id is left out: the flat farm of links named by id holds the file
	// alone, without its folder.
	mountRoot, inMount, known := roots.root(members[0].Path, 0)
	if !known {
		return
	}
	root, err := os.OpenRoot(mountRoot)
	if err != nil {
		return
	}
	defer root.Close()
	dir := path.Dir(inMount)
	folder, err := root.Open(dir)
	if err != nil {
		return
	}
	entries, err := folder.ReadDir(-1)
	folder.Close()
	if err != nil {
		return
	}
	names := make([]string, len(entries))
	infos := make(map[string]os.DirEntry, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
		infos[entry.Name()] = entry
	}
	for _, member := range members {
		if ctx.Err() != nil {
			return
		}
		own, _ := ownSidecars(path.Base(member.Path), names)
		var contents []sidecarContent
		for _, name := range own {
			content, ok := s.sidecarContent(root, path.Join(mountRoot, dir, name), path.Join(dir, name), infos[name])
			if !ok {
				contents = nil
				break
			}
			contents = append(contents, content)
		}
		if len(contents) != len(own) {
			continue
		}
		facts := combineSidecars(contents)
		member.Sidecars = &facts
	}
}

// sidecarContent reads one sidecar, or answers from the cache when the file
// has the size and time it had when it was read.
func (s *Store) sidecarContent(root *os.Root, key, inRoot string, entry os.DirEntry) (sidecarContent, bool) {
	info, err := entry.Info()
	if err != nil || !info.Mode().IsRegular() {
		return sidecarContent{}, false
	}
	if cached, ok := s.sidecarCache.Load(key); ok {
		if hit := cached.(cachedSidecar); hit.size == info.Size() && hit.mod.Equal(info.ModTime()) {
			return hit.content, true
		}
	}
	var content sidecarContent
	// A sidecar too large to be one, or of a kind that records nothing a copy
	// is chosen by, such as an edit list, counts as a sidecar with nothing in
	// it.
	if info.Size() <= sidecarLimit {
		reader := parseSidecarJSON
		switch strings.ToLower(path.Ext(inRoot)) {
		case ".xmp", ".xml":
			reader = parseSidecarXMP
		case ".json":
		default:
			reader = nil
		}
		if reader != nil {
			file, err := root.Open(inRoot)
			if err != nil {
				return sidecarContent{}, false
			}
			body, err := io.ReadAll(io.LimitReader(file, sidecarLimit))
			file.Close()
			if err != nil {
				return sidecarContent{}, false
			}
			content = reader(body)
		}
	}
	s.sidecarCache.Store(key, cachedSidecar{size: info.Size(), mod: info.ModTime(), content: content})
	return content, true
}

// combineSidecars counts what a copy's sidecars record between them. A person
// or keyword named in two of them is counted once, and a keyword that names a
// person is counted as the person.
func combineSidecars(contents []sidecarContent) SidecarFacts {
	facts := SidecarFacts{Files: len(contents)}
	people, keywords := make(map[string]bool), make(map[string]bool)
	for _, content := range contents {
		for _, name := range content.people {
			people[strings.ToLower(name)] = true
		}
		for _, word := range content.keywords {
			keywords[strings.ToLower(word)] = true
		}
		facts.Rating = max(facts.Rating, content.rating)
		facts.Captioned = facts.Captioned || content.captioned
	}
	for word := range keywords {
		if people[word] {
			delete(keywords, word)
		}
	}
	facts.People, facts.Keywords = len(people), len(keywords)
	return facts
}

// What a property means, whichever way a sidecar spells it.
type sidecarField int

const (
	fieldNone sidecarField = iota
	fieldPerson
	fieldKeyword
	fieldRating
	fieldCaption
)

// xmpFields are the XMP properties read, by namespace and name: the people
// Photos, Windows and other tools name, keywords flat or in a hierarchy, the
// rating, and a title or caption.
var xmpFields = map[xml.Name]sidecarField{
	{Space: "http://iptc.org/std/Iptc4xmpExt/2008-02-29/", Local: "PersonInImage"}:     fieldPerson,
	{Space: "http://ns.microsoft.com/photo/1.2/t/Region#", Local: "PersonDisplayName"}: fieldPerson,
	{Space: "http://www.metadataworkinggroup.com/schemas/regions/", Local: "Name"}:     fieldPerson,
	{Space: "http://purl.org/dc/elements/1.1/", Local: "subject"}:                      fieldKeyword,
	{Space: "http://www.digikam.org/ns/1.0/", Local: "TagsList"}:                       fieldKeyword,
	{Space: "http://ns.adobe.com/lightroom/1.0/", Local: "hierarchicalSubject"}:        fieldKeyword,
	{Space: "http://ns.adobe.com/xap/1.0/", Local: "Rating"}:                           fieldRating,
	{Space: "http://purl.org/dc/elements/1.1/", Local: "title"}:                        fieldCaption,
	{Space: "http://purl.org/dc/elements/1.1/", Local: "description"}:                  fieldCaption,
}

// jsonFields are the same properties as the metadata reader writes them to a
// JSON sidecar, without their group, such as XMP:PersonInImage, and as Google
// Takeout writes them.
var jsonFields = map[string]sidecarField{
	"personinimage": fieldPerson, "regionpersondisplayname": fieldPerson, "regionname": fieldPerson,
	"subject": fieldKeyword, "keywords": fieldKeyword, "tagslist": fieldKeyword, "hierarchicalsubject": fieldKeyword, "lastkeywordxmp": fieldKeyword,
	"rating": fieldRating,
	"title":  fieldCaption, "description": fieldCaption, "imagedescription": fieldCaption, "caption-abstract": fieldCaption, "objectname": fieldCaption,
}

// fileLike is a title that only names a file, as Photos and Google write for
// a photo nobody titled: IMG_1234, DSC01234.JPG.
var fileLike = regexp.MustCompile(`(?i)^([a-z_]{2,6}[_-]?\d{3,}.*|[^/\s]+\.[a-z0-9]{2,4})$`)

// add files a value under what it means.
func (c *sidecarContent) add(field sidecarField, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	switch field {
	case fieldPerson:
		c.people = append(c.people, value)
	case fieldKeyword:
		// A hierarchy, such as People|Ana or Places/Home, is the last part.
		if cut := strings.LastIndexAny(value, "|/"); cut >= 0 {
			value = strings.TrimSpace(value[cut+1:])
		}
		if value != "" {
			c.keywords = append(c.keywords, value)
		}
	case fieldRating:
		if rating, err := strconv.ParseFloat(value, 64); err == nil && rating > 0 {
			c.rating = max(c.rating, min(int(rating), 5))
		}
	case fieldCaption:
		if !fileLike.MatchString(value) {
			c.captioned = true
		}
	}
}

// parseSidecarXMP reads an XMP sidecar. A property is either an element
// holding its value, or a list of them, or an attribute of its description;
// all three are read. What is not XMP reads as nothing.
func parseSidecarXMP(body []byte) sidecarContent {
	var content sidecarContent
	decoder := xml.NewDecoder(strings.NewReader(string(body)))
	var open []sidecarField
	for {
		token, err := decoder.Token()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return sidecarContent{}
			}
			return content
		}
		switch element := token.(type) {
		case xml.StartElement:
			for _, attribute := range element.Attr {
				if field := xmpFields[attribute.Name]; field != fieldNone {
					content.add(field, attribute.Value)
				}
			}
			field := xmpFields[element.Name]
			if field == fieldNone && len(open) > 0 {
				field = open[len(open)-1]
			}
			open = append(open, field)
		case xml.EndElement:
			if len(open) > 0 {
				open = open[:len(open)-1]
			}
		case xml.CharData:
			if len(open) > 0 && open[len(open)-1] != fieldNone {
				content.add(open[len(open)-1], string(element))
			}
		}
	}
}

// parseSidecarJSON reads a JSON sidecar: the metadata reader's, one object
// in a list, with names such as XMP:PersonInImage, or Google Takeout's, with
// people as objects that have a name. What is not JSON reads as nothing.
func parseSidecarJSON(body []byte) sidecarContent {
	var value any
	if json.Unmarshal(body, &value) != nil {
		return sidecarContent{}
	}
	var content sidecarContent
	var walk func(value any, field sidecarField)
	walk = func(value any, field sidecarField) {
		switch value := value.(type) {
		case string:
			content.add(field, value)
		case float64:
			if field == fieldRating {
				content.add(field, strconv.FormatFloat(value, 'f', -1, 64))
			}
		case []any:
			for _, item := range value {
				walk(item, field)
			}
		case map[string]any:
			for key, item := range value {
				_, name, grouped := strings.Cut(key, ":")
				if !grouped {
					name = key
				}
				name = strings.ToLower(name)
				switch {
				case jsonFields[name] != fieldNone:
					walk(item, jsonFields[name])
				// A face region, or a person in Google's list, is an object
				// whose name is the person's.
				case name == "name" && field == fieldPerson:
					walk(item, fieldPerson)
				case name == "regionlist" || name == "people":
					walk(item, fieldPerson)
				case field == fieldNone:
					walk(item, fieldNone)
				}
			}
		}
	}
	walk(value, fieldNone)
	return content
}
