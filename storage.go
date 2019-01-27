package main

import (
	"bytes"
	"encoding/gob"
	"io"
	"io/ioutil"
	"os"
	"path"
	"sync"
	"time"
)

var lock sync.Mutex

type marshaller func(v interface{}) (io.Reader, error)
type unmarshaller func(r io.Reader, v interface{}) error

// Load loads the file at path into v.
// Use os.IsNotExist() to see if the returned error is due
// to the file being missing.
func Load(path string, v interface{}, unmarshal unmarshaller) (rerr error) {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		err := f.Close()
		if err != nil {
			rerr = err
		}
	}()
	return unmarshal(f, v)
}

// Save saves a representation of v to the file at path.
func Save(f *os.File, v interface{}, marshal marshaller) (rerr error) {
	r, err := marshal(v)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	return err
}

func GlobEncoder(v interface{}) (io.Reader, error) {
	b := new(bytes.Buffer)

	e := gob.NewEncoder(b)

	err := e.Encode(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func GlobDecoder(r io.Reader, v interface{}) error {
	d := gob.NewDecoder(r)

	err := d.Decode(v)
	if err != nil {
		return err
	}
	return nil
}

type DiskMappedObject struct {
	mapped interface{}
	file   string
	quit   chan struct{}
	lock   sync.RWMutex
}

func (m *DiskMappedObject) Sync() {
	// logger.Debug("Syncing")
	err := m.Write(false)
	if err != nil {
		logger.Fatal(err.Error())
	}
}

func (m *DiskMappedObject) ForceSync() {
	// logger.Debug("Syncing")
	err := m.Write(true)
	if err != nil {
		logger.Fatal(err.Error())
	}
}

func (m *DiskMappedObject) Read() {
	if _, err := os.Stat(m.file); err == nil {
		if err := Load(m.file, m.mapped, GlobDecoder); err != nil {
			logger.Panic(err.Error())
		}
	}
}

func (m *DiskMappedObject) Write(force bool) (rerr error) {
	var err error
	tmpfile, err := ioutil.TempFile("", path.Base(m.file))
	if err != nil {
		return err
	}
	defer func() {
		err := tmpfile.Close()
		if err != nil {
			rerr = err
		}
	}()
	if !force {
		m.lock.RLock()
		err = Save(tmpfile, m.mapped, GlobEncoder)
		m.lock.RUnlock()
	} else {
		err = Save(tmpfile, m.mapped, GlobEncoder)
	}
	if err != nil {
		return err
	}

	err = os.Rename(tmpfile.Name(), m.file)
	if err != nil {
		return err
	}
	return nil
}

func (m *DiskMappedObject) PeriodicWrite(duration time.Duration) error {
	ticker := time.NewTicker(duration)
	m.quit = make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				err := m.Write(false)
				if err != nil {
					logger.Fatal(err.Error())
				}
			case <-m.quit:
				ticker.Stop()
				return
			}
		}
	}()
	return nil
}
