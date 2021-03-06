package data

import (
	"net/http"

	"github.com/cozy/cozy-stack/pkg/couchdb"
	"github.com/cozy/cozy-stack/web/middlewares"
	"github.com/labstack/echo"
)

func proxy(c echo.Context, path string) error {
	doctype := c.Get("doctype").(string)
	instance := middlewares.GetInstance(c)
	p := couchdb.Proxy(instance, doctype, path)
	p.ServeHTTP(c.Response(), c.Request())
	return nil
}

func getDesignDoc(c echo.Context) error {
	docid := c.Param("designdocid")

	revs := c.QueryParam("revs")
	if revs == "true" {
		return proxy(c, "_design/"+docid)
	}

	return c.JSON(http.StatusBadRequest, echo.Map{
		"error": "_design docs are only readable for replication",
	})
}

func getLocalDoc(c echo.Context) error {
	doctype := c.Get("doctype").(string)
	docid := c.Param("docid")

	if err := CheckReadable(c, doctype); err != nil {
		return err
	}

	return proxy(c, "_local/"+docid)

}

func setLocalDoc(c echo.Context) error {
	doctype := c.Get("doctype").(string)
	docid := c.Param("docid")

	if err := CheckReadable(c, doctype); err != nil {
		return err
	}

	return proxy(c, "_local/"+docid)
}

func bulkGet(c echo.Context) error {
	doctype := c.Get("doctype").(string)

	if err := CheckReadable(c, doctype); err != nil {
		return err
	}

	return proxy(c, "_bulk_get")
}

func fullCommit(c echo.Context) error {
	doctype := c.Get("doctype").(string)

	if err := CheckReadable(c, doctype); err != nil {
		return err
	}

	return proxy(c, "_ensure_full_commit")
}

// GetDoc get a doc by its type and id
func dbStatus(c echo.Context) error {
	instance := middlewares.GetInstance(c)
	doctype := c.Get("doctype").(string)

	if err := CheckReadable(c, doctype); err != nil {
		return err
	}

	status, err := couchdb.DBStatus(instance, doctype)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, status)
}

// mostly just to prevent couchdb creash
func dataAPIWelcome(c echo.Context) error {
	return c.JSON(http.StatusOK, echo.Map{
		"message": "welcome to a cozy API",
	})
}

func replicationRoutes(router *echo.Group) {
	// Routes used only for replication
	router.GET("/", dataAPIWelcome)
	router.GET("/:doctype/", dbStatus)
	router.GET("/:doctype/_design/:designdocid", getDesignDoc)
	router.GET("/:doctype/_changes", changesFeed)
	// POST=GET see http://docs.couchdb.org/en/2.0.0/api/database/changes.html#post--db-_changes)
	router.POST("/:doctype/_changes", changesFeed)

	router.POST("/:doctype/_ensure_full_commit", fullCommit)

	// useful for Pouchdb replication
	router.GET("/:doctype/_bulk_get", bulkGet)

	// for storing checkpoints
	router.GET("/:doctype/_local/:docid", getLocalDoc)
	router.PUT("/:doctype/_local/:docid", setLocalDoc)

}
