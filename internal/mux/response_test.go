package mux

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
)

func TestResponseWriter(t *testing.T) {
	t.Parallel()

	var httpRw http.ResponseWriter = &response{}
	_, ok := httpRw.(io.ReaderFrom)
	require.True(t, ok)

	httpRw = httptest.NewRecorder()
	rw := &response{
		ResponseWriter: httpRw,
	}
	require.Equal(t, httpRw, rw.Unwrap())
	require.NoError(t, rw.Error())
	require.Equal(t, int64(0), rw.Size())
	require.Equal(t, http.StatusOK, rw.Status())

	rw = &response{
		ResponseWriter: httptest.NewRecorder(),
	}
	rw.WriteHeader(http.StatusNotFound)
	require.True(t, rw.writtenHeader)
	require.Equal(t, http.StatusNotFound, rw.Status())
	rw.WriteHeader(http.StatusBadGateway)
	require.Equal(t, http.StatusNotFound, rw.Status())
	_, err := rw.Write([]byte("foo"))
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, rw.Status())

	rw = &response{
		ResponseWriter: httptest.NewRecorder(),
	}
	err = errors.New("some server error")
	rw.WriteError(http.StatusInternalServerError, err)
	require.Equal(t, err, rw.Error())
	require.Equal(t, http.StatusInternalServerError, rw.Status())

	rw = &response{
		ResponseWriter: httptest.NewRecorder(),
	}
	first := "hello world"
	n, err := rw.Write([]byte(first))
	require.Equal(t, http.StatusOK, rw.Status())
	require.NoError(t, err)
	require.Equal(t, len(first), n)
	require.Equal(t, int64(len(first)), rw.Size())
	second := "foo bar"
	n, err = rw.Write([]byte(second))
	require.NoError(t, err)
	require.Equal(t, len(second), n)
	require.Equal(t, int64(len(first)+len(second)), rw.Size())

	rw = &response{
		ResponseWriter: httptest.NewRecorder(),
	}
	r := strings.NewReader("reader")
	readFromN, err := rw.ReadFrom(r)
	require.NoError(t, err)
	require.Equal(t, r.Size(), readFromN)
	require.Equal(t, r.Size(), rw.Size())
}

// readerFromRecorder exercises delegation to a writer's optimized copy path.
type readerFromRecorder struct {
	*httptest.ResponseRecorder
	readFromCalls int
}

func (r *readerFromRecorder) ReadFrom(rd io.Reader) (int64, error) {
	r.readFromCalls++
	return io.Copy(r.ResponseRecorder, rd)
}

func TestResponseReadFromStatus(t *testing.T) {
	t.Parallel()
	readErr := errors.New("read failed")

	for _, tc := range []struct {
		name          string
		body          string
		prefix        string
		initialStatus int
		readErr       error
		wantStatus    int
	}{
		{name: "body commits OK", body: "hello", wantStatus: http.StatusOK},
		{name: "empty body permits later header", wantStatus: http.StatusTeapot},
		{name: "error before body permits later header", readErr: readErr, wantStatus: http.StatusTeapot},
		{name: "error after body commits OK", body: "hello", readErr: readErr, wantStatus: http.StatusOK},
		{name: "explicit status is preserved", body: "hello", initialStatus: http.StatusAccepted, wantStatus: http.StatusAccepted},
		{name: "earlier write counts toward size", body: "hello", prefix: "prefix ", wantStatus: http.StatusOK},
	} {
		for _, optimized := range []bool{false, true} {
			writerName := "Writer"
			if optimized {
				writerName = "ReaderFrom"
			}
			for _, path := range []string{"ReadFrom", "ReadFromWriterTo", "CopyReaderFrom", "CopyWriterTo"} {
				t.Run(tc.name+"/"+writerName+"/"+path, func(t *testing.T) {
					recorder := httptest.NewRecorder()
					optimizedWriter := &readerFromRecorder{ResponseRecorder: recorder}
					rw := &response{ResponseWriter: recorder}
					if optimized {
						rw.ResponseWriter = optimizedWriter
					}
					if tc.initialStatus != 0 {
						rw.WriteHeader(tc.initialStatus)
					}
					if tc.prefix != "" {
						_, err := rw.Write([]byte(tc.prefix))
						require.NoError(t, err)
					}

					var source io.Reader = strings.NewReader(tc.body)
					if tc.readErr != nil {
						source = io.MultiReader(source, iotest.ErrReader(tc.readErr))
					}
					if path == "ReadFrom" || path == "CopyReaderFrom" {
						// Hide WriterTo so io.Copy selects the destination's ReadFrom.
						source = struct{ io.Reader }{source}
					}
					var n int64
					var err error
					if path == "ReadFrom" || path == "ReadFromWriterTo" {
						n, err = rw.ReadFrom(source)
					} else {
						n, err = io.Copy(rw, source)
					}
					require.ErrorIs(t, err, tc.readErr)
					require.Equal(t, int64(len(tc.body)), n)
					require.Equal(t, int64(len(tc.prefix+tc.body)), rw.Size())

					rw.WriteHeader(http.StatusTeapot)
					require.Equal(t, tc.wantStatus, recorder.Code)
					require.Equal(t, recorder.Code, rw.Status())
					require.Equal(t, tc.prefix+tc.body, recorder.Body.String())
					if optimized && (path == "ReadFrom" || path == "CopyReaderFrom" || tc.readErr != nil) {
						require.Equal(t, 1, optimizedWriter.readFromCalls)
					} else {
						require.Zero(t, optimizedWriter.readFromCalls)
					}
				})
			}
		}
	}
}
