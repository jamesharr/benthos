package codec

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Jeffail/benthos/v3/internal/docs"
	"github.com/Jeffail/benthos/v3/lib/message"
	"github.com/Jeffail/benthos/v3/lib/types"
)

// ReaderDocs is a static field documentation for input codecs.
var ReaderDocs = docs.FieldCommon(
	"codec", "The way in which the bytes of a data source should be converted into discrete messages, codecs are useful for specifying how large files or contiunous streams of data might be processed in small chunks rather than loading it all in memory. It's possible to consume lines using a custom delimiter with the `delim:x` codec, where x is the character sequence custom delimiter. Codecs can be chained with `/`, for example a gzip compressed CSV file can be consumed with the codec `gzip/csv`.", "lines", "delim:\t", "delim:foobar", "gzip/csv",
).HasAnnotatedOptions(
	"auto", "EXPERIMENTAL: Attempts to derive a codec for each file based on information such as the extension. For example, a .tar.gz file would be consumed with the `gzip/tar` codec. Defaults to all-bytes.",
	"all-bytes", "Consume the entire file as a single binary message.",
	"chunker:x", "Consume the file in chunks of a given number of bytes.",
	"csv", "Consume structured rows as comma separated values, the first row must be a header row.",
	"delim:x", "Consume the file in segments divided by a custom delimiter.",
	"gzip", "Decompress a gzip file, this codec should precede another codec, e.g. `gzip/all-bytes`, `gzip/tar`, `gzip/csv`, etc.",
	"lines", "Consume the file in segments divided by linebreaks.",
	"multipart", "Consumes the output of another codec and batches messages together. A batch ends when an empty message is consumed. For example, the codec `lines/multipart` could be used to consume multipart messages where an empty line indicates the end of each batch.",
	"tar", "Parse the file as a tar archive, and consume each file of the archive as a message.",
)

//------------------------------------------------------------------------------

// ReaderConfig is a general configuration struct that covers all reader codecs.
type ReaderConfig struct {
	MaxScanTokenSize int
}

// NewReaderConfig creates a reader configuration with default values.
func NewReaderConfig() ReaderConfig {
	return ReaderConfig{
		MaxScanTokenSize: bufio.MaxScanTokenSize,
	}
}

//------------------------------------------------------------------------------

// ReaderAckFn is a function provided to a reader codec that it should call once
// the underlying io.ReadCloser is fully consumed.
type ReaderAckFn func(context.Context, error) error

func ackOnce(fn ReaderAckFn) ReaderAckFn {
	var once sync.Once
	return func(ctx context.Context, err error) error {
		var ackErr error
		once.Do(func() {
			ackErr = fn(ctx, err)
		})
		return ackErr
	}
}

// Reader is a codec type that reads message parts from a source.
type Reader interface {
	Next(context.Context) ([]types.Part, ReaderAckFn, error)
	Close(context.Context) error
}

type ioReaderConstructor func(string, io.ReadCloser) (io.ReadCloser, error)

// ReaderConstructor creates a reader from a filename, an io.ReadCloser and an
// ack func which is called by the reader once the io.ReadCloser is finished
// with. The filename can be empty and is usually ignored, but might be
// necessary for certain codecs.
type ReaderConstructor func(string, io.ReadCloser, ReaderAckFn) (Reader, error)

// readerReaderConstructor is a private constructor for readers that _must_
// consume from other readers.
type readerReaderConstructor func(string, Reader) (Reader, error)

func chainIOCtors(first, second ioReaderConstructor) ioReaderConstructor {
	return func(s string, rc io.ReadCloser) (io.ReadCloser, error) {
		r1, err := first(s, rc)
		if err != nil {
			return nil, err
		}
		r2, err := second(s, r1)
		if err != nil {
			r1.Close()
			return nil, err
		}
		return r2, nil
	}
}

func chainIOIntoPartCtor(first ioReaderConstructor, second ReaderConstructor) ReaderConstructor {
	return func(s string, rc io.ReadCloser, aFn ReaderAckFn) (Reader, error) {
		r1, err := first(s, rc)
		if err != nil {
			return nil, err
		}
		r2, err := second(s, r1, aFn)
		if err != nil {
			r1.Close()
			return nil, err
		}
		return r2, nil
	}
}

func chainPartIntoReaderCtor(first ReaderConstructor, second readerReaderConstructor) ReaderConstructor {
	return func(s string, rc io.ReadCloser, aFn ReaderAckFn) (Reader, error) {
		r1, err := first(s, rc, aFn)
		if err != nil {
			return nil, err
		}
		r2, err := second(s, r1)
		if err != nil {
			r1.Close(context.Background())
			return nil, err
		}
		return r2, nil
	}
}

func chainedReader(codec string, conf ReaderConfig) (ReaderConstructor, error) {
	codecs := strings.Split(codec, "/")

	var ioCtor ioReaderConstructor
	var partCtor ReaderConstructor

	for i, codec := range codecs {
		if tmpIOCtor, ok := ioReader(codec, conf); ok {
			if partCtor != nil {
				return nil, fmt.Errorf("unable to follow codec '%v' with '%v'", codecs[i-1], codec)
			}
			if ioCtor != nil {
				ioCtor = chainIOCtors(ioCtor, tmpIOCtor)
			} else {
				ioCtor = tmpIOCtor
			}
			continue
		}
		tmpPartCtor, ok, err := partReader(codec, conf)
		if err != nil {
			return nil, err
		}
		if ok {
			if partCtor != nil {
				return nil, fmt.Errorf("unable to follow codec '%v' with '%v'", codecs[i-1], codec)
			}
			if ioCtor != nil {
				tmpPartCtor = chainIOIntoPartCtor(ioCtor, tmpPartCtor)
				ioCtor = nil
			}
			partCtor = tmpPartCtor
			continue
		}
		tmpReaderCtor, ok := readerReader(codec, conf)
		if !ok {
			return nil, fmt.Errorf("codec was not recognised: %v", codec)
		}
		if partCtor == nil {
			return nil, fmt.Errorf("unable to codec '%v' must be preceded by a structured codec", codec)
		}
		partCtor = chainPartIntoReaderCtor(partCtor, tmpReaderCtor)
	}
	if partCtor == nil {
		return nil, fmt.Errorf("codec was not recognised: %v", codecs)
	}
	return partCtor, nil
}

func ioReader(codec string, conf ReaderConfig) (ioReaderConstructor, bool) {
	if codec == "gzip" {
		return func(_ string, r io.ReadCloser) (io.ReadCloser, error) {
			g, err := gzip.NewReader(r)
			if err != nil {
				r.Close()
				return nil, err
			}
			return g, nil
		}, true
	}
	return nil, false
}

func readerReader(codec string, conf ReaderConfig) (readerReaderConstructor, bool) {
	if codec == "multipart" {
		return func(_ string, r Reader) (Reader, error) {
			return newMultipartReader(r)
		}, true
	}
	return nil, false
}

func partReader(codec string, conf ReaderConfig) (ReaderConstructor, bool, error) {
	switch codec {
	case "all-bytes":
		return func(path string, r io.ReadCloser, fn ReaderAckFn) (Reader, error) {
			return &allBytesReader{r, fn, false}, nil
		}, true, nil
	case "lines":
		return func(path string, r io.ReadCloser, fn ReaderAckFn) (Reader, error) {
			return newLinesReader(conf, r, fn)
		}, true, nil
	case "csv":
		return func(path string, r io.ReadCloser, fn ReaderAckFn) (Reader, error) {
			return newCSVReader(r, fn)
		}, true, nil
	case "tar":
		return newTarReader, true, nil
	}
	if strings.HasPrefix(codec, "delim:") {
		by := strings.TrimPrefix(codec, "delim:")
		if by == "" {
			return nil, false, errors.New("custom delimiter codec requires a non-empty delimiter")
		}
		return func(path string, r io.ReadCloser, fn ReaderAckFn) (Reader, error) {
			return newCustomDelimReader(conf, r, by, fn)
		}, true, nil
	}
	if strings.HasPrefix(codec, "chunker:") {
		chunkSize, err := strconv.ParseUint(strings.TrimPrefix(codec, "chunker:"), 10, 64)
		if err != nil {
			return nil, false, fmt.Errorf("invalid chunk size for chunker codec: %w", err)
		}
		return func(path string, r io.ReadCloser, fn ReaderAckFn) (Reader, error) {
			return newChunkerReader(conf, r, chunkSize, fn)
		}, true, nil
	}
	return nil, false, nil
}

func convertDeprecatedCodec(codec string) string {
	switch codec {
	case "csv-gzip":
		return "gzip/csv"
	case "tar-gzip":
		return "gzip/tar"
	}
	return codec
}

// GetReader returns a constructor that creates reader codecs.
func GetReader(codec string, conf ReaderConfig) (ReaderConstructor, error) {
	codec = convertDeprecatedCodec(codec)
	if codec == "auto" {
		return autoCodec(conf), nil
	}
	return chainedReader(codec, conf)
}

func autoCodec(conf ReaderConfig) ReaderConstructor {
	return func(path string, r io.ReadCloser, fn ReaderAckFn) (Reader, error) {
		codec := "all-bytes"
		switch filepath.Ext(path) {
		case ".csv":
			codec = "csv"
		case ".csv.gz", ".csv.gzip":
			codec = "gzip/csv"
		case ".tar":
			codec = "tar"
		case ".tgz":
			codec = "gzip/tar"
		}
		if strings.HasSuffix(path, ".tar.gzip") {
			codec = "gzip/tar"
		} else if strings.HasSuffix(path, ".tar.gz") {
			codec = "gzip/tar"
		}

		ctor, err := GetReader(codec, conf)
		if err != nil {
			return nil, fmt.Errorf("failed to infer codec: %v", err)
		}
		return ctor(path, r, fn)
	}
}

//------------------------------------------------------------------------------

type allBytesReader struct {
	i        io.ReadCloser
	ack      ReaderAckFn
	consumed bool
}

func (a *allBytesReader) Next(ctx context.Context) ([]types.Part, ReaderAckFn, error) {
	if a.consumed {
		return nil, nil, io.EOF
	}
	a.consumed = true
	b, err := ioutil.ReadAll(a.i)
	if err != nil {
		_ = a.ack(ctx, err)
		return nil, nil, err
	}
	p := message.NewPart(b)
	return []types.Part{p}, a.ack, nil
}

func (a *allBytesReader) Close(ctx context.Context) error {
	if !a.consumed {
		_ = a.ack(ctx, errors.New("service shutting down"))
	}
	return a.i.Close()
}

//------------------------------------------------------------------------------

type linesReader struct {
	buf       *bufio.Scanner
	r         io.ReadCloser
	sourceAck ReaderAckFn

	mut      sync.Mutex
	finished bool
	pending  int32
}

func newLinesReader(conf ReaderConfig, r io.ReadCloser, ackFn ReaderAckFn) (Reader, error) {
	scanner := bufio.NewScanner(r)
	if conf.MaxScanTokenSize != bufio.MaxScanTokenSize {
		scanner.Buffer([]byte{}, conf.MaxScanTokenSize)
	}
	return &linesReader{
		buf:       scanner,
		r:         r,
		sourceAck: ackOnce(ackFn),
	}, nil
}

func (a *linesReader) ack(ctx context.Context, err error) error {
	a.mut.Lock()
	a.pending--
	doAck := a.pending == 0 && a.finished
	a.mut.Unlock()

	if err != nil {
		return a.sourceAck(ctx, err)
	}
	if doAck {
		return a.sourceAck(ctx, nil)
	}
	return nil
}

func (a *linesReader) Next(ctx context.Context) ([]types.Part, ReaderAckFn, error) {
	scanned := a.buf.Scan()
	a.mut.Lock()
	defer a.mut.Unlock()

	if scanned {
		a.pending++
		bytesCopy := make([]byte, len(a.buf.Bytes()))
		copy(bytesCopy, a.buf.Bytes())
		return []types.Part{message.NewPart(bytesCopy)}, a.ack, nil
	}

	err := a.buf.Err()
	if err == nil {
		err = io.EOF
		a.finished = true
	} else {
		_ = a.sourceAck(ctx, err)
	}
	return nil, nil, err
}

func (a *linesReader) Close(ctx context.Context) error {
	a.mut.Lock()
	defer a.mut.Unlock()

	if !a.finished {
		_ = a.sourceAck(ctx, errors.New("service shutting down"))
	}
	if a.pending == 0 {
		_ = a.sourceAck(ctx, nil)
	}
	return a.r.Close()
}

//------------------------------------------------------------------------------

type csvReader struct {
	scanner   *csv.Reader
	r         io.ReadCloser
	sourceAck ReaderAckFn

	headers []string

	mut      sync.Mutex
	finished bool
	pending  int32
}

func newCSVReader(r io.ReadCloser, ackFn ReaderAckFn) (Reader, error) {
	scanner := csv.NewReader(r)
	scanner.ReuseRecord = true

	headers, err := scanner.Read()
	if err != nil {
		return nil, err
	}

	headersCopy := make([]string, len(headers))
	copy(headersCopy, headers)

	return &csvReader{
		scanner:   scanner,
		r:         r,
		sourceAck: ackOnce(ackFn),
		headers:   headersCopy,
	}, nil
}

func (a *csvReader) ack(ctx context.Context, err error) error {
	a.mut.Lock()
	a.pending--
	doAck := a.pending == 0 && a.finished
	a.mut.Unlock()

	if err != nil {
		return a.sourceAck(ctx, err)
	}
	if doAck {
		return a.sourceAck(ctx, nil)
	}
	return nil
}

func (a *csvReader) Next(ctx context.Context) ([]types.Part, ReaderAckFn, error) {
	records, err := a.scanner.Read()

	a.mut.Lock()
	defer a.mut.Unlock()

	if err != nil {
		if err == io.EOF {
			a.finished = true
		} else {
			_ = a.sourceAck(ctx, err)
		}
		return nil, nil, err
	}

	a.pending++

	obj := make(map[string]interface{}, len(records))
	for i, r := range records {
		obj[a.headers[i]] = r
	}

	part := message.NewPart(nil)
	part.SetJSON(obj)

	return []types.Part{part}, a.ack, nil
}

func (a *csvReader) Close(ctx context.Context) error {
	a.mut.Lock()
	defer a.mut.Unlock()

	if !a.finished {
		_ = a.sourceAck(ctx, errors.New("service shutting down"))
	}
	if a.pending == 0 {
		_ = a.sourceAck(ctx, nil)
	}
	return a.r.Close()
}

//------------------------------------------------------------------------------

type customDelimReader struct {
	buf       *bufio.Scanner
	r         io.ReadCloser
	sourceAck ReaderAckFn

	mut      sync.Mutex
	finished bool
	pending  int32
}

func newCustomDelimReader(conf ReaderConfig, r io.ReadCloser, delim string, ackFn ReaderAckFn) (Reader, error) {
	scanner := bufio.NewScanner(r)
	if conf.MaxScanTokenSize != bufio.MaxScanTokenSize {
		scanner.Buffer([]byte{}, conf.MaxScanTokenSize)
	}

	delimBytes := []byte(delim)

	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}

		if i := bytes.Index(data, delimBytes); i >= 0 {
			// We have a full terminated line.
			return i + len(delimBytes), data[0:i], nil
		}

		// If we're at EOF, we have a final, non-terminated line. Return it.
		if atEOF {
			return len(data), data, nil
		}

		// Request more data.
		return 0, nil, nil
	})

	return &customDelimReader{
		buf:       scanner,
		r:         r,
		sourceAck: ackOnce(ackFn),
	}, nil
}

func (a *customDelimReader) ack(ctx context.Context, err error) error {
	a.mut.Lock()
	a.pending--
	doAck := a.pending == 0 && a.finished
	a.mut.Unlock()

	if err != nil {
		return a.sourceAck(ctx, err)
	}
	if doAck {
		return a.sourceAck(ctx, nil)
	}
	return nil
}

func (a *customDelimReader) Next(ctx context.Context) ([]types.Part, ReaderAckFn, error) {
	scanned := a.buf.Scan()

	a.mut.Lock()
	defer a.mut.Unlock()

	if scanned {
		a.pending++

		bytesCopy := make([]byte, len(a.buf.Bytes()))
		copy(bytesCopy, a.buf.Bytes())
		return []types.Part{message.NewPart(bytesCopy)}, a.ack, nil
	}
	err := a.buf.Err()
	if err == nil {
		err = io.EOF
		a.finished = true
	} else {
		_ = a.sourceAck(ctx, err)
	}
	return nil, nil, err
}

func (a *customDelimReader) Close(ctx context.Context) error {
	a.mut.Lock()
	defer a.mut.Unlock()

	if !a.finished {
		_ = a.sourceAck(ctx, errors.New("service shutting down"))
	}
	if a.pending == 0 {
		_ = a.sourceAck(ctx, nil)
	}
	return a.r.Close()
}

//------------------------------------------------------------------------------

type chunkerReader struct {
	chunkSize uint64
	buf       []byte
	r         io.ReadCloser
	sourceAck ReaderAckFn

	mut      sync.Mutex
	finished bool
	pending  int32
}

func newChunkerReader(conf ReaderConfig, r io.ReadCloser, chunkSize uint64, ackFn ReaderAckFn) (Reader, error) {
	return &chunkerReader{
		chunkSize: chunkSize,
		buf:       make([]byte, chunkSize),
		r:         r,
		sourceAck: ackOnce(ackFn),
	}, nil
}

func (a *chunkerReader) ack(ctx context.Context, err error) error {
	a.mut.Lock()
	a.pending--
	doAck := a.pending == 0 && a.finished
	a.mut.Unlock()

	if err != nil {
		return a.sourceAck(ctx, err)
	}
	if doAck {
		return a.sourceAck(ctx, nil)
	}
	return nil
}

func (a *chunkerReader) Next(ctx context.Context) ([]types.Part, ReaderAckFn, error) {
	if a.finished {
		return nil, nil, io.EOF
	}

	n, err := a.r.Read(a.buf)

	a.mut.Lock()
	defer a.mut.Unlock()

	if err != nil {
		if err == io.EOF {
			a.finished = true
		} else {
			_ = a.sourceAck(ctx, err)
			return nil, nil, err
		}
	}

	if n > 0 {
		a.pending++

		bytesCopy := make([]byte, n)
		copy(bytesCopy, a.buf)
		return []types.Part{message.NewPart(bytesCopy)}, a.ack, nil
	}

	return nil, nil, err
}

func (a *chunkerReader) Close(ctx context.Context) error {
	a.mut.Lock()
	defer a.mut.Unlock()

	if !a.finished {
		_ = a.sourceAck(ctx, errors.New("service shutting down"))
	}
	if a.pending == 0 {
		_ = a.sourceAck(ctx, nil)
	}
	return a.r.Close()
}

//------------------------------------------------------------------------------

type tarReader struct {
	buf       *tar.Reader
	r         io.ReadCloser
	sourceAck ReaderAckFn

	mut      sync.Mutex
	finished bool
	pending  int32
}

func newTarReader(path string, r io.ReadCloser, ackFn ReaderAckFn) (Reader, error) {
	return &tarReader{
		buf:       tar.NewReader(r),
		r:         r,
		sourceAck: ackOnce(ackFn),
	}, nil
}

func (a *tarReader) ack(ctx context.Context, err error) error {
	a.mut.Lock()
	a.pending--
	doAck := a.pending == 0 && a.finished
	a.mut.Unlock()

	if err != nil {
		return a.sourceAck(ctx, err)
	}
	if doAck {
		return a.sourceAck(ctx, nil)
	}
	return nil
}

func (a *tarReader) Next(ctx context.Context) ([]types.Part, ReaderAckFn, error) {
	_, err := a.buf.Next()

	a.mut.Lock()
	defer a.mut.Unlock()

	if err == nil {
		fileBuf := bytes.Buffer{}
		if _, err = fileBuf.ReadFrom(a.buf); err != nil {
			_ = a.sourceAck(ctx, err)
			return nil, nil, err
		}
		a.pending++
		return []types.Part{message.NewPart(fileBuf.Bytes())}, a.ack, nil
	}

	if err == io.EOF {
		a.finished = true
	} else {
		_ = a.sourceAck(ctx, err)
	}
	return nil, nil, err
}

func (a *tarReader) Close(ctx context.Context) error {
	a.mut.Lock()
	defer a.mut.Unlock()

	if !a.finished {
		_ = a.sourceAck(ctx, errors.New("service shutting down"))
	}
	if a.pending == 0 {
		_ = a.sourceAck(ctx, nil)
	}
	return a.r.Close()
}

//------------------------------------------------------------------------------

type multipartReader struct {
	child Reader
}

func newMultipartReader(r Reader) (Reader, error) {
	return &multipartReader{
		child: r,
	}, nil
}

func isEmpty(p []types.Part) bool {
	if len(p) == 0 {
		return true
	}
	if len(p) == 1 && len(p[0].Get()) == 0 {
		return true
	}
	return false
}

func (m *multipartReader) Next(ctx context.Context) ([]types.Part, ReaderAckFn, error) {
	var parts []types.Part
	var acks []ReaderAckFn

	ackFn := func(ctx context.Context, err error) error {
		for _, fn := range acks {
			_ = fn(ctx, err)
		}
		return nil
	}

	for {
		newParts, ack, err := m.child.Next(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) && len(parts) > 0 {
				return parts, ackFn, nil
			}
			return nil, nil, err
		}
		if isEmpty(newParts) {
			_ = ack(ctx, nil)
			if len(parts) > 0 {
				// Empty message signals batch end.
				return parts, ackFn, nil
			}
		} else {
			parts = append(parts, newParts...)
			acks = append(acks, ack)
		}
	}
}

func (m *multipartReader) Close(ctx context.Context) error {
	return m.child.Close(ctx)
}
