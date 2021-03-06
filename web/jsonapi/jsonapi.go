// Package jsonapi is for using the JSON-API format: parsing, serialization,
// checking the content-type, etc.
package jsonapi

import (
	"encoding/json"
	"net/http"

	"github.com/labstack/echo"
)

// ContentType is the official mime-type for JSON-API
const ContentType = "application/vnd.api+json"

// Document is JSON-API document, identified by the mediatype
// application/vnd.api+json
// See http://jsonapi.org/format/#document-structure
type Document struct {
	Data     *json.RawMessage `json:"data,omitempty"`
	Errors   ErrorList        `json:"errors,omitempty"`
	Links    *LinksList       `json:"links,omitempty"`
	Included []interface{}    `json:"included,omitempty"`
}

// Data can be called to send an answer with a JSON-API document containing a
// single object as data
func Data(c echo.Context, statusCode int, o Object, links *LinksList) error {
	var included []interface{}
	for _, o := range o.Included() {
		data, err := MarshalObject(o)
		if err != nil {
			return InternalServerError(err)
		}
		included = append(included, &data)
	}
	data, err := MarshalObject(o)
	if err != nil {
		return InternalServerError(err)
	}
	doc := Document{
		Data:     &data,
		Links:    links,
		Included: included,
	}

	resp := c.Response()
	resp.Header().Set("Content-Type", ContentType)
	resp.WriteHeader(statusCode)
	return json.NewEncoder(resp).Encode(doc)
}

// DataList can be called to send an multiple-value answer with a
// JSON-API document contains multiple objects.
func DataList(c echo.Context, statusCode int, objs []Object, links *LinksList) error {
	objsMarshaled := make([]json.RawMessage, len(objs))
	for i, o := range objs {
		j, err := MarshalObject(o)
		if err != nil {
			return InternalServerError(err)
		}
		objsMarshaled[i] = j
	}

	data, err := json.Marshal(objsMarshaled)
	if err != nil {
		return InternalServerError(err)
	}

	doc := Document{
		Data:  (*json.RawMessage)(&data),
		Links: links,
	}

	resp := c.Response()
	resp.Header().Set("Content-Type", ContentType)
	resp.WriteHeader(statusCode)
	return json.NewEncoder(resp).Encode(doc)
}

// DataError can be called to send an error answer with a JSON-API document
// containing a single value error.
func DataError(c echo.Context, err *Error) error {
	doc := Document{
		Errors: ErrorList{err},
	}
	resp := c.Response()
	resp.Header().Set("Content-Type", ContentType)
	resp.WriteHeader(err.Status)
	return json.NewEncoder(resp).Encode(doc)
}

// DataErrorList can be called to send an error answer with a JSON-API document
// containing multiple errors.
func DataErrorList(c echo.Context, errs ...*Error) error {
	doc := Document{
		Errors: errs,
	}
	resp := c.Response()
	resp.Header().Set("Content-Type", ContentType)
	resp.WriteHeader(errs[0].Status)
	return json.NewEncoder(resp).Encode(doc)
}

// Bind is used to unmarshal an input JSONApi document. It binds an
// incoming request to a attribute type.
func Bind(req *http.Request, attrs interface{}) (*ObjectMarshalling, error) {
	decoder := json.NewDecoder(req.Body)
	var doc *Document
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	if doc.Data == nil {
		return nil, BadJSON()
	}
	var obj *ObjectMarshalling
	if err := json.Unmarshal(*doc.Data, &obj); err != nil {
		return nil, err
	}
	if obj.Attributes != nil {
		if err := json.Unmarshal(*obj.Attributes, &attrs); err != nil {
			return nil, err
		}
	}
	return obj, nil
}

// BindRelations extracts a Relationships request ( a list of ResourceIdentifier)
func BindRelations(req *http.Request) ([]ResourceIdentifier, error) {
	var out []ResourceIdentifier
	decoder := json.NewDecoder(req.Body)
	var doc *Document
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	if doc.Data == nil {
		return nil, BadJSON()
	}
	// Attempt Unmarshaling either as ResourceIdentifier or []ResourceIdentifier
	if err := json.Unmarshal(*doc.Data, &out); err != nil {
		var ri ResourceIdentifier
		if err = json.Unmarshal(*doc.Data, &ri); err != nil {
			return nil, err
		}
		out = []ResourceIdentifier{ri}
		return out, nil
	}
	return out, nil
}
