package permissions

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/cozy/cozy-stack/pkg/config"
	"github.com/cozy/cozy-stack/pkg/crypto"
	"github.com/cozy/cozy-stack/pkg/instance"
	"github.com/cozy/cozy-stack/pkg/permissions"
	"github.com/labstack/echo"
	"github.com/stretchr/testify/assert"
	jwt "gopkg.in/dgrijalva/jwt-go.v3"
)

var ts *httptest.Server
var token string
var testInstance *instance.Instance

func TestMain(m *testing.M) {
	config.UseTestFile()

	var testInstance = &instance.Instance{
		OAuthSecret: []byte("topsecret"),
		Domain:      "example.com",
	}

	token, _ = crypto.NewJWT(testInstance.OAuthSecret, permissions.Claims{
		StandardClaims: jwt.StandardClaims{
			Audience: permissions.AccessTokenAudience,
			Issuer:   testInstance.Domain,
			IssuedAt: crypto.Timestamp(),
			Subject:  "fakeapp",
		},
		Scope: "io.cozy.contacts io.cozy.files:GET",
	})

	handler := echo.New()

	group := handler.Group("/permissions")
	group.Use(injectInstance(testInstance))
	group.Use(Extractor)
	Routes(group)

	ts = httptest.NewServer(handler)
	res := m.Run()
	ts.Close()
	os.Exit(res)
}

func TestGetPermissions(t *testing.T) {

	req, _ := http.NewRequest("GET", ts.URL+"/permissions/self", nil)
	req.Header.Add("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if !assert.NoError(t, err) {
		return
	}
	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	if !assert.NoError(t, err) {
		return
	}
	var out map[string]interface{}
	err = json.Unmarshal(body, &out)

	assert.NoError(t, err)
	assert.Equal(t, "200 OK", res.Status, "should get a 200")
	for key, r := range out {
		rule := r.(map[string]interface{})
		if key == "rule0" {
			assert.Equal(t, "io.cozy.contacts", rule["type"])
		} else {
			assert.Equal(t, "io.cozy.files", rule["type"])
			assert.Equal(t, []interface{}{"GET"}, rule["verbs"])
		}
	}
}

func TestBadPermissionsBearer(t *testing.T) {
	req, _ := http.NewRequest("GET", ts.URL+"/permissions/self", nil)
	req.Header.Add("Authorization", "Bearer garbage")
	res, err := http.DefaultClient.Do(req)
	if !assert.NoError(t, err) {
		return
	}
	defer res.Body.Close()
	assert.Equal(t, res.StatusCode, http.StatusBadRequest)
}

func injectInstance(i *instance.Instance) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("instance", i)
			return next(c)
		}
	}
}
