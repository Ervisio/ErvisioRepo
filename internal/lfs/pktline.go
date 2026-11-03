package lfs

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// pkt-line framing for git-lfs-transfer.

const (
	pktMaxData = 65516
	pktFlush   = "0000"
	pktDelim   = "0001"
)

type pktKind int

const (
	pktData pktKind = iota
	pktFlushKind
	pktDelimKind
)

var errPktFormat = errors.New("malformed pkt-line")

type pktReader struct {
	r   io.Reader
	buf []byte
}

func newPktReader(r io.Reader) *pktReader { return &pktReader{r: r, buf: make([]byte, pktMaxData+4)} }

// next reads one packet; the payload is valid until the next call.
func (p *pktReader) next() (pktKind, []byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(p.r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n, err := strconv.ParseUint(string(hdr[:]), 16, 16)
	if err != nil {
		return 0, nil, errPktFormat
	}
	switch {
	case n == 0:
		return pktFlushKind, nil, nil
	case n == 1:
		return pktDelimKind, nil, nil
	case n < 4 || n > pktMaxData+4:
		return 0, nil, errPktFormat
	}
	b := p.buf[:n-4]
	if _, err := io.ReadFull(p.r, b); err != nil {
		return 0, nil, fmt.Errorf("read pkt-line: %w", err)
	}
	return pktData, b, nil
}

// lines reads text packets up to a flush or delim and reports which ended
// them (true for delim).
func (p *pktReader) lines() ([]string, bool, error) {
	var out []string
	for {
		k, b, err := p.next()
		if err != nil {
			return nil, false, err
		}
		switch k {
		case pktFlushKind:
			return out, false, nil
		case pktDelimKind:
			return out, true, nil
		}
		if len(out) >= 10000 {
			return nil, false, errPktFormat
		}
		out = append(out, strings.TrimSuffix(string(b), "\n"))
	}
}

// dataReader streams data packets up to the next flush.
type dataReader struct {
	p    *pktReader
	rest []byte
	done bool
	err  error
}

func (d *dataReader) Read(b []byte) (int, error) {
	for len(d.rest) == 0 {
		if d.err != nil {
			return 0, d.err
		}
		if d.done {
			return 0, io.EOF
		}
		k, pkt, err := d.p.next()
		switch {
		case err != nil:
			d.err = err
		case k == pktFlushKind:
			d.done = true
		case k == pktDelimKind:
			d.err = errPktFormat
		default:
			d.rest = pkt
		}
	}
	n := copy(b, d.rest)
	d.rest = d.rest[n:]
	return n, nil
}

// drain consumes the stream up to the flush.
func (d *dataReader) drain() error {
	_, err := io.Copy(io.Discard, d)
	return err
}

type pktWriter struct {
	w   io.Writer
	err error
}

func (p *pktWriter) raw(s string) {
	if p.err == nil {
		_, p.err = io.WriteString(p.w, s)
	}
}

func (p *pktWriter) data(b []byte) {
	for len(b) > 0 && p.err == nil {
		n := min(len(b), pktMaxData)
		p.raw(fmt.Sprintf("%04x", n+4))
		if p.err == nil {
			_, p.err = p.w.Write(b[:n])
		}
		b = b[n:]
	}
}

func (p *pktWriter) line(s string) { p.data([]byte(s + "\n")) }
func (p *pktWriter) flush()        { p.raw(pktFlush) }
func (p *pktWriter) delim()        { p.raw(pktDelim) }

// Write streams b as data packets.
func (p *pktWriter) Write(b []byte) (int, error) {
	p.data(b)
	if p.err != nil {
		return 0, p.err
	}
	return len(b), nil
}
