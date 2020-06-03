// +build integration

package integration_test

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gorestful "github.com/emicklei/go-restful"
	"github.com/gin-gonic/gin"
	"github.com/julienschmidt/httprouter"
	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/negroni"
	"goji.io"
	"goji.io/pat"

	metricsprometheus "github.com/slok/go-http-metrics/metrics/prometheus"
	"github.com/slok/go-http-metrics/middleware"
	echomiddleware "github.com/slok/go-http-metrics/middleware/echo"
	ginmiddleware "github.com/slok/go-http-metrics/middleware/gin"
	gojimiddleware "github.com/slok/go-http-metrics/middleware/goji"
	gorestfulmiddleware "github.com/slok/go-http-metrics/middleware/gorestful"
	httproutermiddleware "github.com/slok/go-http-metrics/middleware/httprouter"
	negronimiddleware "github.com/slok/go-http-metrics/middleware/negroni"
	stdmiddleware "github.com/slok/go-http-metrics/middleware/std"
)

// handlerConfig is the configuration the servers will need to set up to properly
// execute the tests.
type handlerConfig struct {
	Path           string
	Code           int
	Method         string
	ReturnData     string
	SleepDuration  time.Duration
	NumberRequests int
}

func TestMiddlewarePrometheus(t *testing.T) {
	tests := map[string]struct {
		handler func(m middleware.Middleware, hc []handlerConfig) http.Handler
	}{
		"STD http.Handler": {handler: prepareHandlerSTD},
		"Negroni":          {handler: prepareHandlerNegroni},
		"HTTPRouter":       {handler: prepareHandlerHTTPRouter},
		"Gorestful":        {handler: prepareHandlerGorestful},
		"Gin":              {handler: prepareHandlerGin},
		"Echo":             {handler: prepareHandlerEcho},
		"Goji":             {handler: prepareHandlerGoji},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// Setup.
			reg := prometheus.NewRegistry()
			rec := metricsprometheus.NewRecorder(metricsprometheus.Config{
				Registry:        reg,
				DurationBuckets: []float64{0.05, 0.1, 0.15, 0.2},
				SizeBuckets:     []float64{1, 2, 3, 4, 5},
			})
			mdlw := middleware.New(middleware.Config{
				Service:  "integration",
				Recorder: rec,
			})

			serverHandler := test.handler(mdlw, expReqs)
			metricsHandler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})

			// Test.
			testMiddlewareRequests(t, serverHandler, expReqs)
			testMiddlewarePrometheusMetrics(t, metricsHandler, expMetrics)
		})
	}
}

func testMiddlewareRequests(t *testing.T, h http.Handler, expReqs []handlerConfig) {
	require := require.New(t)
	assert := assert.New(t)

	// Setup server.
	server := httptest.NewServer(h)
	t.Cleanup(func() { server.Close() })

	// Make all the requests.
	for _, config := range expReqs {
		for i := 0; i < config.NumberRequests; i++ {
			r, err := http.NewRequest(config.Method, server.URL+config.Path, nil)
			require.NoError(err)
			resp, err := http.DefaultClient.Do(r)
			require.NoError(err)

			// Check.
			assert.Equal(config.Code, resp.StatusCode)
			b, err := ioutil.ReadAll(resp.Body)
			require.NoError(err)
			assert.Equal(config.ReturnData, string(b))
		}
	}
}

func testMiddlewarePrometheusMetrics(t *testing.T, h http.Handler, expMetrics []string) {
	require := require.New(t)
	assert := assert.New(t)

	// Setup server.
	server := httptest.NewServer(h)
	t.Cleanup(func() { server.Close() })

	// Get metrics.
	r, err := http.NewRequest(http.MethodGet, server.URL+"/metrics", nil)
	require.NoError(err)
	resp, err := http.DefaultClient.Do(r)
	require.NoError(err)

	// Check.
	b, err := ioutil.ReadAll(resp.Body)
	require.NoError(err)
	metrics := string(b)

	// Make all the requests.
	for _, expMetric := range expMetrics {
		assert.Contains(metrics, expMetric)
	}
}

func prepareHandlerSTD(m middleware.Middleware, hc []handlerConfig) http.Handler {
	// Setup handlers.
	mux := http.NewServeMux()
	for _, h := range hc {
		h := h
		mux.Handle(h.Path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != h.Method {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}

			time.Sleep(h.SleepDuration)
			w.WriteHeader(h.Code)
			w.Write([]byte(h.ReturnData)) // nolint: errcheck
		}))
	}

	// Setup server and middleware.
	h := stdmiddleware.Handler("", m, mux)

	return h
}

func prepareHandlerNegroni(m middleware.Middleware, hc []handlerConfig) http.Handler {
	// Setup handlers.
	mux := http.NewServeMux()
	for _, h := range hc {
		h := h
		mux.Handle(h.Path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != h.Method {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}

			time.Sleep(h.SleepDuration)
			w.WriteHeader(h.Code)
			w.Write([]byte(h.ReturnData)) // nolint: errcheck
		}))
	}

	// Setup server and middleware.
	n := negroni.Classic()
	n.Use(negronimiddleware.Handler("", m))
	n.UseHandler(mux)

	return n
}

func prepareHandlerHTTPRouter(m middleware.Middleware, hc []handlerConfig) http.Handler {
	r := httprouter.New()

	// Setup handlers.
	for _, h := range hc {
		h := h
		hr := func(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
			time.Sleep(h.SleepDuration)
			w.WriteHeader(h.Code)
			w.Write([]byte(h.ReturnData)) // nolint: errcheck
		}

		// Setup middleware on each of the routes.
		r.Handle(h.Method, h.Path, httproutermiddleware.Handler("", hr, m))
	}

	return r
}

func prepareHandlerGorestful(m middleware.Middleware, hc []handlerConfig) http.Handler {
	// Setup server and middleware.
	c := gorestful.NewContainer()
	c.Filter(gorestfulmiddleware.Handler("", m))

	// Setup handlers.
	ws := &gorestful.WebService{}
	for _, h := range hc {
		h := h
		ws.Route(ws.Method(h.Method).Path(h.Path).To(func(_ *gorestful.Request, resp *gorestful.Response) {
			time.Sleep(h.SleepDuration)
			resp.WriteHeader(h.Code)
			resp.Write([]byte(h.ReturnData)) // nolint: errcheck
		}))
	}
	c.Add(ws)

	return c
}

func prepareHandlerGin(m middleware.Middleware, hc []handlerConfig) http.Handler {
	// Setup server and middleware.
	e := gin.New()
	e.Use(ginmiddleware.Handler("", m))

	// Setup handlers.
	for _, h := range hc {
		h := h
		e.Handle(h.Method, h.Path, func(c *gin.Context) {
			time.Sleep(h.SleepDuration)
			c.String(h.Code, h.ReturnData)
		})
	}

	return e
}

func prepareHandlerEcho(m middleware.Middleware, hc []handlerConfig) http.Handler {
	// Setup server and middleware.
	e := echo.New()
	e.Use(echomiddleware.Handler("", m))

	// Setup handlers.
	for _, h := range hc {
		h := h
		e.Add(h.Method, h.Path, func(c echo.Context) error {
			time.Sleep(h.SleepDuration)
			return c.String(h.Code, h.ReturnData)
		})
	}

	return e
}

func prepareHandlerGoji(m middleware.Middleware, hc []handlerConfig) http.Handler {
	// Setup server and middleware.
	mux := goji.NewMux()
	mux.Use(gojimiddleware.Handler("", m))

	// Setup handlers.
	for _, h := range hc {
		h := h
		mux.HandleFunc(pat.NewWithMethods(h.Path, h.Method), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(h.SleepDuration)
			w.WriteHeader(h.Code)
			w.Write([]byte(h.ReturnData)) // nolint: errcheck
		}))
	}

	return mux
}
