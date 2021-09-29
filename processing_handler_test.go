package main

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/imgproxy/imgproxy/v2/config"
	"github.com/imgproxy/imgproxy/v2/config/configurators"
	"github.com/imgproxy/imgproxy/v2/imagedata"
	"github.com/imgproxy/imgproxy/v2/imagemeta"
	"github.com/imgproxy/imgproxy/v2/imagetype"
	"github.com/imgproxy/imgproxy/v2/router"
	"github.com/imgproxy/imgproxy/v2/vips"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type ProcessingHandlerTestSuite struct {
	suite.Suite

	router *router.Router
}

func (s *ProcessingHandlerTestSuite) SetupSuite() {
	config.Reset()

	wd, err := os.Getwd()
	assert.Nil(s.T(), err)

	config.LocalFileSystemRoot = filepath.Join(wd, "/testdata")

	logrus.SetOutput(ioutil.Discard)

	initialize()

	s.router = buildRouter()
}

func (s *ProcessingHandlerTestSuite) TeardownSuite() {
	shutdown()
	logrus.SetOutput(os.Stdout)
}

func (s *ProcessingHandlerTestSuite) SetupTest() {
	// We don't need config.LocalFileSystemRoot anymore as it is used
	// only during initialization
	config.Reset()
}

func (s *ProcessingHandlerTestSuite) send(path string, header ...http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rw := httptest.NewRecorder()

	if len(header) > 0 {
		req.Header = header[0]
	}

	s.router.ServeHTTP(rw, req)

	return rw
}

func (s *ProcessingHandlerTestSuite) readTestFile(name string) []byte {
	wd, err := os.Getwd()
	assert.Nil(s.T(), err)

	data, err := ioutil.ReadFile(filepath.Join(wd, "testdata", name))
	assert.Nil(s.T(), err)

	return data
}

func (s *ProcessingHandlerTestSuite) readBody(res *http.Response) []byte {
	data, err := ioutil.ReadAll(res.Body)
	assert.Nil(s.T(), err)
	return data
}

func (s *ProcessingHandlerTestSuite) TestRequest() {
	rw := s.send("/unsafe/rs:fill:4:4/plain/local:///test1.png")
	res := rw.Result()

	assert.Equal(s.T(), 200, res.StatusCode)
	assert.Equal(s.T(), "image/png", res.Header.Get("Content-Type"))

	meta, err := imagemeta.DecodeMeta(res.Body)

	assert.Nil(s.T(), err)
	assert.Equal(s.T(), imagetype.PNG, meta.Format())
	assert.Equal(s.T(), 4, meta.Width())
	assert.Equal(s.T(), 4, meta.Height())
}

func (s *ProcessingHandlerTestSuite) TestSignatureValidationFailure() {
	config.Keys = [][]byte{[]byte("test-key")}
	config.Salts = [][]byte{[]byte("test-salt")}

	rw := s.send("/unsafe/rs:fill:4:4/plain/local:///test1.png")
	res := rw.Result()

	assert.Equal(s.T(), 403, res.StatusCode)
}

func (s *ProcessingHandlerTestSuite) TestSignatureValidationSuccess() {
	config.Keys = [][]byte{[]byte("test-key")}
	config.Salts = [][]byte{[]byte("test-salt")}

	rw := s.send("/My9d3xq_PYpVHsPrCyww0Kh1w5KZeZhIlWhsa4az1TI/rs:fill:4:4/plain/local:///test1.png")
	res := rw.Result()

	assert.Equal(s.T(), 200, res.StatusCode)
}

func (s *ProcessingHandlerTestSuite) TestSourceValidation() {
	imagedata.RedirectAllRequestsTo("local:///test1.png")
	defer imagedata.StopRedirectingRequests()

	tt := []struct {
		name           string
		allowedSources []string
		requestPath    string
		expectedError  bool
	}{
		{
			name:           "match http URL without wildcard",
			allowedSources: []string{"local://", "http://images.dev/"},
			requestPath:    "/unsafe/plain/http://images.dev/lorem/ipsum.jpg",
			expectedError:  false,
		},
		{
			name:           "match http URL with wildcard in hostname single level",
			allowedSources: []string{"local://", "http://*.mycdn.dev/"},
			requestPath:    "/unsafe/plain/http://a-1.mycdn.dev/lorem/ipsum.jpg",
			expectedError:  false,
		},
		{
			name:           "match http URL with wildcard in hostname multiple levels",
			allowedSources: []string{"local://", "http://*.mycdn.dev/"},
			requestPath:    "/unsafe/plain/http://a-1.b-2.mycdn.dev/lorem/ipsum.jpg",
			expectedError:  false,
		},
		{
			name:           "no match s3 URL with allowed local and http URLs",
			allowedSources: []string{"local://", "http://images.dev/"},
			requestPath:    "/unsafe/plain/s3://images/lorem/ipsum.jpg",
			expectedError:  true,
		},
		{
			name:           "no match http URL with wildcard in hostname including slash",
			allowedSources: []string{"local://", "http://*.mycdn.dev/"},
			requestPath:    "/unsafe/plain/http://other.dev/.mycdn.dev/lorem/ipsum.jpg",
			expectedError:  true,
		},
	}

	for _, tc := range tt {
		s.T().Run(tc.name, func(t *testing.T) {
			exps := make([]*regexp.Regexp, len(tc.allowedSources))
			for i, pattern := range tc.allowedSources {
				exps[i] = configurators.RegexpFromPattern(pattern)
			}
			config.AllowedSources = exps

			rw := s.send(tc.requestPath)
			res := rw.Result()

			if tc.expectedError {
				assert.Equal(s.T(), 404, res.StatusCode)
			} else {
				assert.Equal(s.T(), 200, res.StatusCode)
			}
		})
	}
}

func (s *ProcessingHandlerTestSuite) TestSourceFormatNotSupported() {
	vips.DisableLoadSupport(imagetype.PNG)
	defer vips.ResetLoadSupport()

	rw := s.send("/unsafe/rs:fill:4:4/plain/local:///test1.png")
	res := rw.Result()

	assert.Equal(s.T(), 422, res.StatusCode)
}

func (s *ProcessingHandlerTestSuite) TestResultingFormatNotSupported() {
	vips.DisableSaveSupport(imagetype.PNG)
	defer vips.ResetSaveSupport()

	rw := s.send("/unsafe/rs:fill:4:4/plain/local:///test1.png@png")
	res := rw.Result()

	assert.Equal(s.T(), 422, res.StatusCode)
}

func (s *ProcessingHandlerTestSuite) TestSkipProcessingConfig() {
	config.SkipProcessingFormats = []imagetype.Type{imagetype.PNG}

	rw := s.send("/unsafe/rs:fill:4:4/plain/local:///test1.png")
	res := rw.Result()

	assert.Equal(s.T(), 200, res.StatusCode)

	actual := s.readBody(res)
	expected := s.readTestFile("test1.png")

	assert.True(s.T(), bytes.Equal(expected, actual))
}

func (s *ProcessingHandlerTestSuite) TestSkipProcessingPO() {
	rw := s.send("/unsafe/rs:fill:4:4/skp:png/plain/local:///test1.png")
	res := rw.Result()

	assert.Equal(s.T(), 200, res.StatusCode)

	actual := s.readBody(res)
	expected := s.readTestFile("test1.png")

	assert.True(s.T(), bytes.Equal(expected, actual))
}

func (s *ProcessingHandlerTestSuite) TestSkipProcessingSameFormat() {
	config.SkipProcessingFormats = []imagetype.Type{imagetype.PNG}

	rw := s.send("/unsafe/rs:fill:4:4/plain/local:///test1.png@png")
	res := rw.Result()

	assert.Equal(s.T(), 200, res.StatusCode)

	actual := s.readBody(res)
	expected := s.readTestFile("test1.png")

	assert.True(s.T(), bytes.Equal(expected, actual))
}

func (s *ProcessingHandlerTestSuite) TestSkipProcessingDifferentFormat() {
	config.SkipProcessingFormats = []imagetype.Type{imagetype.PNG}

	rw := s.send("/unsafe/rs:fill:4:4/plain/local:///test1.png@jpg")
	res := rw.Result()

	assert.Equal(s.T(), 200, res.StatusCode)

	actual := s.readBody(res)
	expected := s.readTestFile("test1.png")

	assert.False(s.T(), bytes.Equal(expected, actual))
}

func (s *ProcessingHandlerTestSuite) TestSkipProcessingSVG() {
	rw := s.send("/unsafe/rs:fill:4:4/plain/local:///test1.svg")
	res := rw.Result()

	assert.Equal(s.T(), 200, res.StatusCode)

	actual := s.readBody(res)
	expected := s.readTestFile("test1.svg")

	assert.True(s.T(), bytes.Equal(expected, actual))
}

func (s *ProcessingHandlerTestSuite) TestNotSkipProcessingSVGToJPG() {
	rw := s.send("/unsafe/rs:fill:4:4/plain/local:///test1.svg@jpg")
	res := rw.Result()

	assert.Equal(s.T(), 200, res.StatusCode)

	actual := s.readBody(res)
	expected := s.readTestFile("test1.svg")

	assert.False(s.T(), bytes.Equal(expected, actual))
}

func (s *ProcessingHandlerTestSuite) TestErrorSavingToSVG() {
	rw := s.send("/unsafe/rs:fill:4:4/plain/local:///test1.png@svg")
	res := rw.Result()

	assert.Equal(s.T(), 422, res.StatusCode)
}

func (s *ProcessingHandlerTestSuite) TestCacheControlPassthrough() {
	config.CacheControlPassthrough = true

	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "fake-cache-control")
		rw.Header().Set("Expires", "fake-expires")
		rw.WriteHeader(200)
		rw.Write(s.readTestFile("test1.png"))
	}))
	defer ts.Close()

	rw := s.send("/unsafe/rs:fill:4:4/plain/" + ts.URL)
	res := rw.Result()

	assert.Equal(s.T(), "fake-cache-control", res.Header.Get("Cache-Control"))
	assert.Equal(s.T(), "fake-expires", res.Header.Get("Expires"))
}

func (s *ProcessingHandlerTestSuite) TestCacheControlPassthroughDisabled() {
	config.CacheControlPassthrough = false

	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "fake-cache-control")
		rw.Header().Set("Expires", "fake-expires")
		rw.WriteHeader(200)
		rw.Write(s.readTestFile("test1.png"))
	}))
	defer ts.Close()

	rw := s.send("/unsafe/rs:fill:4:4/plain/" + ts.URL)
	res := rw.Result()

	assert.NotEqual(s.T(), "fake-cache-control", res.Header.Get("Cache-Control"))
	assert.NotEqual(s.T(), "fake-expires", res.Header.Get("Expires"))
}

func (s *ProcessingHandlerTestSuite) TestETagDisabled() {
	config.ETagEnabled = false

	rw := s.send("/unsafe/rs:fill:4:4/plain/local:///test1.png")
	res := rw.Result()

	assert.Equal(s.T(), 200, res.StatusCode)
	assert.Empty(s.T(), res.Header.Get("ETag"))
}

func (s *ProcessingHandlerTestSuite) TestETagReqNoIfNotModified() {
	config.ETagEnabled = true

	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		assert.Empty(s.T(), r.Header.Get("If-None-Match"))

		rw.Header().Set("ETag", `"loremipsumdolor"`)
		rw.WriteHeader(200)
		rw.Write(s.readTestFile("test1.png"))
	}))
	defer ts.Close()

	rw := s.send("/unsafe/rs:fill:4:4/plain/" + ts.URL)
	res := rw.Result()

	assert.Equal(s.T(), 200, res.StatusCode)
	assert.Equal(
		s.T(),
		`"1Uuny6YTSUO08MMVZyantp9oaoOcsaYibQoNbBTJEzk/RImxvcmVtaXBzdW1kb2xvciI"`,
		res.Header.Get("ETag"),
	)
}

func (s *ProcessingHandlerTestSuite) TestETagDataNoIfNotModified() {
	config.ETagEnabled = true

	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		assert.Empty(s.T(), r.Header.Get("If-None-Match"))

		rw.WriteHeader(200)
		rw.Write(s.readTestFile("test1.png"))
	}))
	defer ts.Close()

	rw := s.send("/unsafe/rs:fill:4:4/plain/" + ts.URL)
	res := rw.Result()

	assert.Equal(s.T(), 200, res.StatusCode)
	assert.Equal(
		s.T(),
		`"1Uuny6YTSUO08MMVZyantp9oaoOcsaYibQoNbBTJEzk/Dl29MNvkqdLEqPyFvMlh_NlPbRrsC0GDG_AUlmMdX6HA"`,
		res.Header.Get("ETag"),
	)
}

func (s *ProcessingHandlerTestSuite) TestETagReqMatch() {
	config.ETagEnabled = true

	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		assert.Equal(s.T(), `"loremipsumdolor"`, r.Header.Get("If-None-Match"))

		rw.WriteHeader(304)
	}))
	defer ts.Close()

	etag := `"1Uuny6YTSUO08MMVZyantp9oaoOcsaYibQoNbBTJEzk/RImxvcmVtaXBzdW1kb2xvciI"`

	header := make(http.Header)
	header.Set("If-None-Match", etag)

	rw := s.send("/unsafe/rs:fill:4:4/plain/"+ts.URL, header)
	res := rw.Result()

	assert.Equal(s.T(), 304, res.StatusCode)
	assert.Equal(s.T(), etag, res.Header.Get("ETag"))
}

func (s *ProcessingHandlerTestSuite) TestETagDataMatch() {
	config.ETagEnabled = true

	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		assert.Empty(s.T(), r.Header.Get("If-None-Match"))

		rw.WriteHeader(200)
		rw.Write(s.readTestFile("test1.png"))
	}))
	defer ts.Close()

	etag := `"1Uuny6YTSUO08MMVZyantp9oaoOcsaYibQoNbBTJEzk/Dl29MNvkqdLEqPyFvMlh_NlPbRrsC0GDG_AUlmMdX6HA"`

	header := make(http.Header)
	header.Set("If-None-Match", etag)

	rw := s.send("/unsafe/rs:fill:4:4/plain/"+ts.URL, header)
	res := rw.Result()

	assert.Equal(s.T(), 304, res.StatusCode)
	assert.Equal(s.T(), etag, res.Header.Get("ETag"))
}

func (s *ProcessingHandlerTestSuite) TestETagReqNotMatch() {
	config.ETagEnabled = true

	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		assert.Equal(s.T(), `"loremipsum"`, r.Header.Get("If-None-Match"))

		rw.Header().Set("ETag", `"loremipsumdolor"`)
		rw.WriteHeader(200)
		rw.Write(s.readTestFile("test1.png"))
	}))
	defer ts.Close()

	header := make(http.Header)
	header.Set("If-None-Match", `"1Uuny6YTSUO08MMVZyantp9oaoOcsaYibQoNbBTJEzk/RImxvcmVtaXBzdW0i"`)

	rw := s.send("/unsafe/rs:fill:4:4/plain/"+ts.URL, header)
	res := rw.Result()

	assert.Equal(s.T(), 200, res.StatusCode)
	assert.Equal(
		s.T(),
		`"1Uuny6YTSUO08MMVZyantp9oaoOcsaYibQoNbBTJEzk/RImxvcmVtaXBzdW1kb2xvciI"`,
		res.Header.Get("ETag"),
	)
}

func (s *ProcessingHandlerTestSuite) TestETagDataNotMatch() {
	config.ETagEnabled = true

	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		assert.Empty(s.T(), r.Header.Get("If-None-Match"))

		rw.WriteHeader(200)
		rw.Write(s.readTestFile("test1.png"))
	}))
	defer ts.Close()

	header := make(http.Header)
	header.Set("If-None-Match", `"1Uuny6YTSUO08MMVZyantp9oaoOcsaYibQoNbBTJEzk/Dl29MNvkqdLEq"`)

	rw := s.send("/unsafe/rs:fill:4:4/plain/"+ts.URL, header)
	res := rw.Result()

	assert.Equal(s.T(), 200, res.StatusCode)
	assert.Equal(
		s.T(),
		`"1Uuny6YTSUO08MMVZyantp9oaoOcsaYibQoNbBTJEzk/Dl29MNvkqdLEqPyFvMlh_NlPbRrsC0GDG_AUlmMdX6HA"`,
		res.Header.Get("ETag"),
	)
}

func (s *ProcessingHandlerTestSuite) TestETagProcessingOptionsNotMatch() {
	config.ETagEnabled = true

	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		assert.Empty(s.T(), r.Header.Get("If-None-Match"))

		rw.Header().Set("ETag", `"loremipsumdolor"`)
		rw.WriteHeader(200)
		rw.Write(s.readTestFile("test1.png"))
	}))
	defer ts.Close()

	header := make(http.Header)
	header.Set("If-None-Match", `"1Uuny6YTSUO08MMVZ/Dl29MNvkqdLEq"`)

	rw := s.send("/unsafe/rs:fill:4:4/plain/"+ts.URL, header)
	res := rw.Result()

	assert.Equal(s.T(), 200, res.StatusCode)
	assert.Equal(
		s.T(),
		`"1Uuny6YTSUO08MMVZyantp9oaoOcsaYibQoNbBTJEzk/RImxvcmVtaXBzdW1kb2xvciI"`,
		res.Header.Get("ETag"),
	)
}

func TestProcessingHandler(t *testing.T) {
	suite.Run(t, new(ProcessingHandlerTestSuite))
}