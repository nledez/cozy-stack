package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/cozy/cozy-stack/pkg/config"
	"github.com/cozy/cozy-stack/pkg/instance"
	"github.com/cozy/cozy-stack/web/middlewares"
	"github.com/labstack/echo"
	"github.com/stretchr/testify/assert"
)

const domain = "cozy.example.net"

var ts *httptest.Server

func TestSetupAssets(t *testing.T) {
	e := echo.New()
	err := SetupAssets(e, "../assets")
	if !assert.NoError(t, err) {
		return
	}

	ts := httptest.NewServer(e)
	defer ts.Close()

	res, _ := http.Get(ts.URL + "/assets/images/cozy.svg")
	defer res.Body.Close()
	assert.Equal(t, 200, res.StatusCode)
}

func TestSetupAssetsStatik(t *testing.T) {
	e := echo.New()
	err := SetupAssets(e, "")
	if !assert.NoError(t, err) {
		return
	}

	ts := httptest.NewServer(e)
	defer ts.Close()

	res, _ := http.Get(ts.URL + "/assets/images/cozy.svg")
	defer res.Body.Close()
	assert.Equal(t, 200, res.StatusCode)
}

func TestSetupRoutes(t *testing.T) {
	e := echo.New()
	err := SetupRoutes(e)
	if !assert.NoError(t, err) {
		return
	}

	ts := httptest.NewServer(e)
	defer ts.Close()

	res, _ := http.Get(ts.URL + "/version")
	defer res.Body.Close()
	assert.Equal(t, 200, res.StatusCode)
}

func TestParseHost(t *testing.T) {
	apis := echo.New()

	apis.GET("/", func(c echo.Context) error {
		instance := middlewares.GetInstance(c)
		assert.NotNil(t, instance, "the instance should have been set in the echo context")
		return c.String(http.StatusOK, "OK")
	}, middlewares.NeedInstance)

	router, err := CreateSubdomainProxy(apis, func(c echo.Context) error {
		slug := c.Get("slug").(string)
		return c.String(200, "OK:"+slug)
	})

	if !assert.NoError(t, err) {
		return
	}

	urls := map[string]string{
		"https://" + domain + "/":        "OK",
		"https://foo." + domain + "/app": "OK:foo",
		"https://bar." + domain + "/app": "OK:bar",
	}

	for u, k := range urls {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", u, nil)
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, k, w.Body.String())
	}
}

func TestMain(m *testing.M) {
	config.UseTestFile()
	config.GetConfig().Assets = "../assets"
	instance.Destroy(domain)
	instance.Create(&instance.Options{
		Domain: domain,
		Locale: "en",
	})
	res := m.Run()
	instance.Destroy(domain)
	os.Exit(res)
}
