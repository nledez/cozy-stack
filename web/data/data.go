// Package data provide simple CRUD operation on couchdb doc
package data

import (
	"net/http"
	"strconv"

	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/web/jsonapi"
	"github.com/cozy/cozy-stack/web/middlewares"
	"github.com/labstack/echo"
)

func validDoctype(next echo.HandlerFunc) echo.HandlerFunc {
	// TODO extends me to verificate characters allowed in db name.
	return func(c echo.Context) error {
		doctype := c.Param("doctype")
		if doctype == "" && c.Path() != "/data/" {
			return jsonapi.NewError(http.StatusBadRequest, "Invalid doctype '%s'", doctype)
		}
		c.Set("doctype", doctype)
		return next(c)
	}
}

// GetDoc get a doc by its type and id
func getDoc(c echo.Context) error {
	instance := middlewares.GetInstance(c)
	doctype := c.Get("doctype").(string)
	docid := c.Param("docid")

	if err := CheckReadable(c, doctype); err != nil {
		return err
	}

	if docid == "" {
		return dbStatus(c)
	}

	revs := c.QueryParam("revs")
	if revs == "true" {
		return proxy(c, docid)
	}

	var out couchdb.JSONDoc
	err := couchdb.GetDoc(instance, doctype, docid, &out)
	if err != nil {
		return err
	}

	out.Type = doctype
	return c.JSON(http.StatusOK, out.ToMapWithType())
}

// CreateDoc create doc from the json passed as body
func createDoc(c echo.Context) error {
	doctype := c.Get("doctype").(string)
	instance := middlewares.GetInstance(c)

	doc := couchdb.JSONDoc{Type: doctype}
	if err := c.Bind(&doc.M); err != nil {
		return jsonapi.NewError(http.StatusBadRequest, err)
	}

	if err := CheckWritable(c, doctype); err != nil {
		return err
	}

	if err := couchdb.CreateDoc(instance, doc); err != nil {
		return err
	}

	return c.JSON(http.StatusCreated, echo.Map{
		"ok":   true,
		"id":   doc.ID(),
		"rev":  doc.Rev(),
		"type": doc.DocType(),
		"data": doc.ToMapWithType(),
	})
}

func updateDoc(c echo.Context) error {
	instance := middlewares.GetInstance(c)

	var doc couchdb.JSONDoc
	if err := c.Bind(&doc); err != nil {
		return jsonapi.NewError(http.StatusBadRequest, err)
	}

	doc.Type = c.Param("doctype")

	if err := CheckWritable(c, doc.Type); err != nil {
		return err
	}

	if (doc.ID() == "") != (doc.Rev() == "") {
		return jsonapi.NewError(http.StatusBadRequest,
			"You must either provide an _id and _rev in document (update) or neither (create with  fixed id).")
	}

	if doc.ID() != "" && doc.ID() != c.Param("docid") {
		return jsonapi.NewError(http.StatusBadRequest, "document _id doesnt match url")
	}

	var err error
	if doc.ID() == "" {
		doc.SetID(c.Param("docid"))
		err = couchdb.CreateNamedDoc(instance, doc)
	} else {
		err = couchdb.UpdateDoc(instance, doc)
	}

	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, echo.Map{
		"ok":   true,
		"id":   doc.ID(),
		"rev":  doc.Rev(),
		"type": doc.DocType(),
		"data": doc.ToMapWithType(),
	})
}

func deleteDoc(c echo.Context) error {
	instance := middlewares.GetInstance(c)
	doctype := c.Get("doctype").(string)
	docid := c.Param("docid")
	revHeader := c.Request().Header.Get("If-Match")
	revQuery := c.QueryParam("rev")
	rev := ""

	if revHeader != "" && revQuery != "" && revQuery != revHeader {
		return jsonapi.NewError(http.StatusBadRequest,
			"If-Match Header and rev query parameters mismatch")
	} else if revHeader != "" {
		rev = revHeader
	} else if revQuery != "" {
		rev = revQuery
	} else {
		return jsonapi.NewError(http.StatusBadRequest, "delete without revision")
	}

	if err := CheckWritable(c, doctype); err != nil {
		return err
	}

	tombrev, err := couchdb.Delete(instance, doctype, docid, rev)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, echo.Map{
		"ok":      true,
		"id":      docid,
		"rev":     tombrev,
		"type":    doctype,
		"deleted": true,
	})

}

func defineIndex(c echo.Context) error {
	instance := middlewares.GetInstance(c)
	doctype := c.Get("doctype").(string)
	var definitionRequest map[string]interface{}

	if err := c.Bind(&definitionRequest); err != nil {
		return jsonapi.NewError(http.StatusBadRequest, err)
	}

	if err := CheckReadable(c, doctype); err != nil {
		return err
	}

	result, err := couchdb.DefineIndexRaw(instance, doctype, &definitionRequest)
	if couchdb.IsNoDatabaseError(err) {
		if err = couchdb.CreateDB(instance, doctype); err == nil {
			result, err = couchdb.DefineIndexRaw(instance, doctype, &definitionRequest)
		}
	}
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, result)
}

func findDocuments(c echo.Context) error {
	instance := middlewares.GetInstance(c)
	doctype := c.Get("doctype").(string)
	var findRequest map[string]interface{}

	if err := c.Bind(&findRequest); err != nil {
		return jsonapi.NewError(http.StatusBadRequest, err)
	}

	if err := CheckReadable(c, doctype); err != nil {
		return err
	}

	var results []couchdb.JSONDoc
	err := couchdb.FindDocsRaw(instance, doctype, &findRequest, &results)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, echo.Map{"docs": results})
}

var allowedChangesParams = map[string]bool{
	"feed":      true,
	"style":     true,
	"since":     true,
	"limit":     true,
	"timeout":   true,
	"heartbeat": true, // Pouchdb sends heartbeet even for non-continuous
}

func changesFeed(c echo.Context) error {
	instance := middlewares.GetInstance(c)

	// Drop a clear error for parameters not supported by stack
	for key := range c.QueryParams() {
		if !allowedChangesParams[key] {
			return jsonapi.NewError(http.StatusBadRequest, "Unsuported query parameter '%s'", key)
		}
	}

	feed, err := couchdb.ValidChangesMode(c.QueryParam("feed"))
	if err != nil {
		return jsonapi.NewError(http.StatusBadRequest, err)
	}

	feedStyle, err := couchdb.ValidChangesStyle(c.QueryParam("style"))
	if err != nil {
		return jsonapi.NewError(http.StatusBadRequest, err)
	}

	limitString := c.QueryParam("limit")
	limit := 0
	if limitString != "" {
		if limit, err = strconv.Atoi(limitString); err != nil {
			return jsonapi.NewError(http.StatusBadRequest, "Invalid limit value '%s'", err.Error())
		}
	}

	results, err := couchdb.GetChanges(instance, &couchdb.ChangesRequest{
		DocType: c.Get("doctype").(string),
		Feed:    feed,
		Style:   feedStyle,
		Since:   c.QueryParam("since"),
		Limit:   limit,
	})

	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, results)
}

func allDocs(c echo.Context) error {
	doctype := c.Get("doctype").(string)

	if err := CheckReadable(c, doctype); err != nil {
		return err
	}

	return proxy(c, "_all_docs")
}

func couchdbStyleErrorHandler(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		err := next(c)
		if err == nil {
			return nil
		}

		if ce, ok := err.(*couchdb.Error); ok {
			return c.JSON(ce.StatusCode, ce.JSON())
		}

		if he, ok := err.(*echo.HTTPError); ok {
			return c.JSON(he.Code, echo.Map{"error": he.Error()})
		}

		if je, ok := err.(*jsonapi.Error); ok {
			return c.JSON(je.Status, echo.Map{"error": je.Title})
		}

		return c.JSON(http.StatusInternalServerError, echo.Map{
			"error": err.Error(),
		})
	}
}

// Routes sets the routing for the status service
func Routes(router *echo.Group) {
	router.Use(validDoctype)
	router.Use(couchdbStyleErrorHandler)

	replicationRoutes(router)

	// API Routes
	router.GET("/:doctype/:docid", getDoc)
	router.PUT("/:doctype/:docid", updateDoc)
	router.DELETE("/:doctype/:docid", deleteDoc)
	router.POST("/:doctype/:docid/relationships/references", addReferencesHandler)
	router.POST("/:doctype/", createDoc)
	router.GET("/:doctype/_all_docs", allDocs)
	router.POST("/:doctype/_all_docs", allDocs)
	router.POST("/:doctype/_index", defineIndex)
	router.POST("/:doctype/_find", findDocuments)
	// router.DELETE("/:doctype/:docid", DeleteDoc)
}
