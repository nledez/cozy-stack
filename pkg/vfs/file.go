package vfs

import (
	"bytes"
	"crypto/md5" // #nosec
	"encoding/base64"
	"fmt"
	"hash"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/cozy/cozy-stack/pkg/consts"
	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/pkg/couchdb/mango"
	"github.com/cozy/cozy-stack/web/jsonapi"
	"github.com/spf13/afero"
)

// FileDoc is a struct containing all the informations about a file.
// It implements the couchdb.Doc and jsonapi.Object interfaces.
type FileDoc struct {
	// Type of document. Useful to (de)serialize and filter the data
	// from couch.
	Type string `json:"type"`
	// Qualified file identifier
	DocID string `json:"_id,omitempty"`
	// File revision
	DocRev string `json:"_rev,omitempty"`
	// File name
	Name string `json:"name"`
	// Parent directory identifier
	DirID       string `json:"dir_id,omitempty"`
	RestorePath string `json:"restore_path,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Size       int64    `json:"size,string"` // Serialized in JSON as a string, because JS has some issues with big numbers
	MD5Sum     []byte   `json:"md5sum"`
	Mime       string   `json:"mime"`
	Class      string   `json:"class"`
	Executable bool     `json:"executable"`
	Tags       []string `json:"tags"`

	ReferencedBy []jsonapi.ResourceIdentifier `json:"referenced_by,omitempty"`

	parent *DirDoc
}

// ID returns the file qualified identifier
func (f *FileDoc) ID() string { return f.DocID }

// Rev returns the file revision
func (f *FileDoc) Rev() string { return f.DocRev }

// DocType returns the file document type
func (f *FileDoc) DocType() string { return consts.Files }

// SetID changes the file qualified identifier
func (f *FileDoc) SetID(id string) { f.DocID = id }

// SetRev changes the file revision
func (f *FileDoc) SetRev(rev string) { f.DocRev = rev }

// Path is used to generate the file path
func (f *FileDoc) Path(c Context) (string, error) {
	var parentPath string
	if f.DirID == consts.RootDirID {
		parentPath = "/"
	} else {
		parent, err := f.Parent(c)
		if err != nil {
			return "", err
		}
		parentPath, err = parent.Path(c)
		if err != nil {
			return "", err
		}
	}
	return path.Join(parentPath, f.Name), nil
}

// Parent returns the parent directory document
func (f *FileDoc) Parent(c Context) (*DirDoc, error) {
	parent, err := getParentDir(c, f.parent, f.DirID)
	if err != nil {
		return nil, err
	}
	f.parent = parent
	return parent, nil
}

// Links is used to generate a JSON-API link for the file (part of
// jsonapi.Object interface)
func (f *FileDoc) Links() *jsonapi.LinksList {
	return &jsonapi.LinksList{Self: "/files/" + f.DocID}
}

func (f *FileDoc) parentRelationShip() jsonapi.Relationship {
	return jsonapi.Relationship{
		Links: &jsonapi.LinksList{
			Related: "/files/" + f.DirID,
		},
		Data: jsonapi.ResourceIdentifier{
			ID:   f.DirID,
			Type: consts.Files,
		},
	}
}

func (f *FileDoc) referencedByRelationShip() jsonapi.Relationship {
	return jsonapi.Relationship{
		Links: &jsonapi.LinksList{
			Self: "/files/" + f.ID() + "/relationships/references",
		},
		Data: f.ReferencedBy,
	}
}

// Relationships is used to generate the parent relationship in JSON-API format
// (part of the jsonapi.Object interface)
func (f *FileDoc) Relationships() jsonapi.RelationshipMap {
	return jsonapi.RelationshipMap{
		"parent":        f.parentRelationShip(),
		"referenced_by": f.referencedByRelationShip(),
	}
}

// HideFields returns a jsonapi.Object which serialize like the original
// file but without the ReferencedBy field
func (f *FileDoc) HideFields() jsonapi.Object {
	return &struct {
		ReferencedBy []jsonapi.ResourceIdentifier `json:"referenced_by,omitempty"`
		*FileDoc
	}{
		FileDoc:      f,
		ReferencedBy: nil,
	}
}

// Included is part of the jsonapi.Object interface
func (f *FileDoc) Included() []jsonapi.Object {
	return []jsonapi.Object{}
}

// AddReferencedBy adds referenced_by to the file
func (f *FileDoc) AddReferencedBy(ri ...jsonapi.ResourceIdentifier) {
	f.ReferencedBy = append(f.ReferencedBy, ri...)
}

// NewFileDoc is the FileDoc constructor. The given name is validated.
func NewFileDoc(name, dirID string, size int64, md5Sum []byte, mime, class string, cdate time.Time, executable bool, tags []string) (*FileDoc, error) {
	if err := checkFileName(name); err != nil {
		return nil, err
	}

	if dirID == "" {
		dirID = consts.RootDirID
	}

	tags = uniqueTags(tags)

	doc := &FileDoc{
		Type:  consts.FileType,
		Name:  name,
		DirID: dirID,

		CreatedAt:  cdate,
		UpdatedAt:  cdate,
		Size:       size,
		MD5Sum:     md5Sum,
		Mime:       mime,
		Class:      class,
		Executable: executable,
		Tags:       tags,
	}

	return doc, nil
}

// GetFileDoc is used to fetch file document information form the
// database.
func GetFileDoc(c Context, fileID string) (*FileDoc, error) {
	doc := &FileDoc{}
	err := couchdb.GetDoc(c, consts.Files, fileID, doc)
	if err != nil {
		return nil, err
	}
	if doc.Type != consts.FileType {
		return nil, os.ErrNotExist
	}
	return doc, nil
}

// GetFileDocFromPath is used to fetch file document information from
// the database from its path.
func GetFileDocFromPath(c Context, name string) (*FileDoc, error) {
	if !path.IsAbs(name) {
		return nil, ErrNonAbsolutePath
	}

	var err error
	dirpath := path.Dir(name)
	var parent *DirDoc
	parent, err = GetDirDocFromPath(c, dirpath, false)

	if err != nil {
		return nil, err
	}

	dirID := parent.ID()
	selector := mango.Map{
		"dir_id": dirID,
		"name":   path.Base(name),
		"type":   consts.FileType,
	}

	var docs []*FileDoc
	req := &couchdb.FindRequest{
		Selector: selector,
		Limit:    1,
	}
	err = couchdb.FindDocs(c, consts.Files, req, &docs)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, os.ErrNotExist
	}

	fileDoc := docs[0]
	fileDoc.parent = parent

	return fileDoc, nil
}

// ServeFileContent replies to a http request using the content of a
// file given its FileDoc.
//
// It uses internally http.ServeContent and benefits from it by
// offering support to Range, If-Modified-Since and If-None-Match
// requests. It uses the revision of the file as the Etag value for
// non-ranged requests
//
// The content disposition is inlined.
func ServeFileContent(c Context, doc *FileDoc, disposition string, req *http.Request, w http.ResponseWriter) error {
	header := w.Header()
	header.Set("Content-Type", doc.Mime)
	if disposition != "" {
		header.Set("Content-Disposition", ContentDisposition(disposition, doc.Name))
	}

	if header.Get("Range") == "" {
		eTag := base64.StdEncoding.EncodeToString(doc.MD5Sum)
		header.Set("Etag", eTag)
	}

	name, err := doc.Path(c)
	if err != nil {
		return err
	}

	content, err := c.FS().Open(name)
	if err != nil {
		return err
	}
	defer content.Close()

	http.ServeContent(w, req, doc.Name, doc.UpdatedAt, content)
	return nil
}

// File represents a file handle. It can be used either for writing OR
// reading, but not both at the same time.
type File struct {
	c  Context       // vfs context
	f  afero.File    // file handle
	fc *fileCreation // file creation handle
}

// fileCreation represents a file open for writing. It is used to
// create of file or to modify the content of a file.
//
// fileCreation implements io.WriteCloser.
type fileCreation struct {
	w         int64     // total size written
	newdoc    *FileDoc  // new document
	olddoc    *FileDoc  // old document if any
	newpath   string    // file new path
	bakpath   string    // backup file path in case of modifying an existing file
	checkHash bool      // whether or not we need the assert the hash is good
	hash      hash.Hash // hash we build up along the file
	err       error     // write error
}

// Open returns a file handle that can be used to read form the file
// specified by the given document.
func Open(c Context, doc *FileDoc) (*File, error) {
	name, err := doc.Path(c)
	if err != nil {
		return nil, err
	}
	f, err := c.FS().Open(name)
	if err != nil {
		return nil, err
	}
	return &File{c, f, nil}, nil
}

// CreateFile is used to create file or modify an existing file
// content. It returns a fileCreation handle. Along with the vfs
// context, it receives the new file document that you want to create.
// It can also receive the old document, representing the current
// revision of the file. In this case it will try to modify the file,
// otherwise it will create it.
//
// Warning: you MUST call the Close() method and check for its error.
// The Close() method will actually create or update the document in
// couchdb. It will also check the md5 hash if required.
func CreateFile(c Context, newdoc, olddoc *FileDoc) (*File, error) {
	newpath, err := newdoc.Path(c)
	if err != nil {
		return nil, err
	}

	var bakpath string
	if olddoc != nil {
		bakpath = fmt.Sprintf("/.%s_%s", olddoc.ID(), olddoc.Rev())
		if err = safeRenameFile(c, newpath, bakpath); err != nil {
			// in case of a concurrent access to this method, it can happend
			// that the file has already been renamed. In this case the
			// safeRenameFile will return an os.ErrNotExist error. But this
			// error is misleading since it does not reflect the conflict.
			if os.IsNotExist(err) {
				err = ErrConflict
			}
			return nil, err
		}
	}

	if olddoc != nil {
		newdoc.SetID(olddoc.ID())
		newdoc.SetRev(olddoc.Rev())
		newdoc.CreatedAt = olddoc.CreatedAt
		newdoc.UpdatedAt = time.Now()
	}

	f, err := safeCreateFile(newpath, newdoc.Executable, c.FS())
	if err != nil {
		return nil, err
	}

	hash := md5.New() // #nosec

	fc := &fileCreation{
		w: 0,

		newdoc:  newdoc,
		olddoc:  olddoc,
		bakpath: bakpath,
		newpath: newpath,

		checkHash: newdoc.MD5Sum != nil,
		hash:      hash,
	}

	return &File{c, f, fc}, nil
}

// Read bytes from the file into given buffer - part of io.Reader
// This method can be called on read mode only
func (f *File) Read(p []byte) (int, error) {
	if f.fc != nil {
		return 0, os.ErrInvalid
	}
	return f.f.Read(p)
}

// Seek into the file - part of io.Reader
// This method can be called on read mode only
func (f *File) Seek(offset int64, whence int) (int64, error) {
	if f.fc != nil {
		return 0, os.ErrInvalid
	}
	return f.f.Seek(offset, whence)
}

// Write bytes to the file - part of io.WriteCloser
// This method can be called in write mode only
func (f *File) Write(p []byte) (int, error) {
	if f.fc == nil {
		return 0, os.ErrInvalid
	}

	n, err := f.f.Write(p)
	if err != nil {
		f.fc.err = err
		return n, err
	}

	f.fc.w += int64(n)

	_, err = f.fc.hash.Write(p)
	return n, err
}

// Close the handle and commit the document in database if all checks
// are OK. It is important to check errors returned by this method.
func (f *File) Close() error {
	if f.fc == nil {
		return f.f.Close()
	}

	var err error
	fc, c := f.fc, f.c

	defer func() {
		werr := fc.err
		if fc.olddoc != nil {
			// put back backup file revision in case on error occured while
			// modifying file content or remove the backup file otherwise
			if err != nil || werr != nil {
				c.FS().Rename(fc.bakpath, fc.newpath)
			} else {
				c.FS().Remove(fc.bakpath)
			}
		} else if err != nil || werr != nil {
			// remove file if an error occured while file creation
			c.FS().Remove(fc.newpath)
		}
	}()

	err = f.f.Close()
	if err != nil {
		return err
	}

	newdoc, olddoc, written := fc.newdoc, fc.olddoc, fc.w

	md5sum := fc.hash.Sum(nil)
	if fc.checkHash && !bytes.Equal(newdoc.MD5Sum, md5sum) {
		err = ErrInvalidHash
		return err
	}

	if newdoc.Size < 0 {
		newdoc.Size = written
	}

	if newdoc.MD5Sum == nil {
		newdoc.MD5Sum = md5sum
	}

	if newdoc.Size != written {
		err = ErrContentLengthMismatch
		return err
	}

	if olddoc != nil {
		err = couchdb.UpdateDoc(c, newdoc)
	} else {
		err = couchdb.CreateDoc(c, newdoc)
	}

	return err
}

// ModifyFileMetadata modify the metadata associated to a file. It can
// be used to rename or move the file in the VFS.
func ModifyFileMetadata(c Context, olddoc *FileDoc, patch *DocPatch) (*FileDoc, error) {
	var err error
	cdate := olddoc.CreatedAt
	patch, err = normalizeDocPatch(&DocPatch{
		Name:        &olddoc.Name,
		DirID:       &olddoc.DirID,
		RestorePath: &olddoc.RestorePath,
		Tags:        &olddoc.Tags,
		UpdatedAt:   &olddoc.UpdatedAt,
		Executable:  &olddoc.Executable,
	}, patch, cdate)

	if err != nil {
		return nil, err
	}

	newdoc, err := NewFileDoc(
		*patch.Name,
		*patch.DirID,
		olddoc.Size,
		olddoc.MD5Sum,
		olddoc.Mime,
		olddoc.Class,
		cdate,
		*patch.Executable,
		*patch.Tags,
	)
	if err != nil {
		return nil, err
	}

	newdoc.RestorePath = *patch.RestorePath

	var parent *DirDoc
	if newdoc.DirID != olddoc.DirID {
		parent, err = newdoc.Parent(c)
	} else {
		parent = olddoc.parent
	}

	if err != nil {
		return nil, err
	}

	newdoc.SetID(olddoc.ID())
	newdoc.SetRev(olddoc.Rev())
	newdoc.UpdatedAt = *patch.UpdatedAt
	newdoc.parent = parent

	oldpath, err := olddoc.Path(c)
	if err != nil {
		return nil, err
	}
	newpath, err := newdoc.Path(c)
	if err != nil {
		return nil, err
	}

	if newpath != oldpath {
		err = safeRenameFile(c, oldpath, newpath)
		if err != nil {
			return nil, err
		}
	}

	if newdoc.Executable != olddoc.Executable {
		err = c.FS().Chmod(newpath, getFileMode(newdoc.Executable))
		if err != nil {
			return nil, err
		}
	}

	err = couchdb.UpdateDoc(c, newdoc)
	return newdoc, err
}

// TrashFile is used to delete a file given its document
func TrashFile(c Context, olddoc *FileDoc) (*FileDoc, error) {
	oldpath, err := olddoc.Path(c)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(oldpath, TrashDirName) {
		return nil, ErrFileInTrash
	}

	trashDirID := consts.TrashDirID
	restorePath := path.Dir(oldpath)

	var newdoc *FileDoc
	tryOrUseSuffix(olddoc.Name, conflictFormat, func(name string) error {
		newdoc, err = ModifyFileMetadata(c, olddoc, &DocPatch{
			DirID:       &trashDirID,
			RestorePath: &restorePath,
			Name:        &name,
		})
		return err
	})
	return newdoc, err
}

// RestoreFile is used to restore a trashed file given its document
func RestoreFile(c Context, olddoc *FileDoc) (*FileDoc, error) {
	oldpath, err := olddoc.Path(c)
	if err != nil {
		return nil, err
	}
	restoreDir, err := getRestoreDir(c, oldpath, olddoc.RestorePath)
	if err != nil {
		return nil, err
	}
	var newdoc *FileDoc
	var emptyStr string
	name := stripSuffix(olddoc.Name, conflictSuffix)
	tryOrUseSuffix(name, "%s (%s)", func(name string) error {
		newdoc, err = ModifyFileMetadata(c, olddoc, &DocPatch{
			DirID:       &restoreDir.DocID,
			RestorePath: &emptyStr,
			Name:        &name,
		})
		return err
	})
	return newdoc, err
}

// DestroyFile definitively destroy a file from the trash.
func DestroyFile(c Context, doc *FileDoc) error {
	path, err := doc.Path(c)
	if err != nil {
		return err
	}

	err = c.FS().Remove(path)
	if err != nil {
		return err
	}

	return couchdb.DeleteDoc(c, doc)
}

func safeCreateFile(name string, executable bool, fs afero.Fs) (afero.File, error) {
	// write only (O_WRONLY), try to create the file and check that it
	// does not already exist (O_CREATE|O_EXCL).
	flag := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	mode := getFileMode(executable)
	return fs.OpenFile(name, flag, mode)
}

func safeRenameFile(c Context, oldpath, newpath string) error {
	newpath = path.Clean(newpath)
	oldpath = path.Clean(oldpath)

	if !path.IsAbs(newpath) || !path.IsAbs(oldpath) {
		return ErrNonAbsolutePath
	}

	_, err := c.FS().Stat(newpath)
	if err == nil {
		return os.ErrExist
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return c.FS().Rename(oldpath, newpath)
}

func getFileMode(executable bool) os.FileMode {
	if executable {
		return 0755 // -rwxr-xr-x
	}
	return 0644 // -rw-r--r--
}

var (
	_ couchdb.Doc    = &FileDoc{}
	_ jsonapi.Object = &FileDoc{}
)
