// Copyright 2023 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package sstable

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/cockroachdb/pebble/bloom"
	"github.com/cockroachdb/pebble/internal/base"
	"github.com/cockroachdb/pebble/objstorage/objstorageprovider"
	"github.com/cockroachdb/pebble/vfs"
)

// testKVs is a key-value map holding test data.
type testKVs map[string]string

// SortedKeys returns the keys in the map, in sorted order.
func (m testKVs) SortedKeys() []string {
	res := make([]string, 0, len(m))
	for k := range m {
		res = append(res, k)
	}
	sort.Strings(res)
	return res
}

// These variable should not be used directly, only via hamletWordCount().
var hamletWordCountState struct {
	once sync.Once
	data testKVs
}

// hamletWordCount returns the data in testdata.h/txt, as a map from word to
// count (as string).
func hamletWordCount() testKVs {
	hamletWordCountState.once.Do(func() {
		wordCount := make(map[string]string)
		f, err := os.Open(filepath.FromSlash("testdata/h.txt"))
		if err != nil {
			panic(err)
		}
		defer f.Close()
		r := bufio.NewReader(f)

		for {
			s, err := r.ReadBytes('\n')
			if err == io.EOF {
				break
			}
			if err != nil {
				panic(err)
			}
			k := strings.TrimSpace(string(s[8:]))
			v := strings.TrimSpace(string(s[:8]))
			wordCount[k] = v
		}
		if len(wordCount) != 1710 {
			panic(fmt.Sprintf("h.txt entry count: got %d, want %d", len(wordCount), 1710))
		}
		for _, s := range hamletNonsenseWords {
			if _, ok := wordCount[s]; ok {
				panic(fmt.Sprintf("nonsense word %q was in h.txt", s))
			}
		}
		hamletWordCountState.data = wordCount
	})
	return hamletWordCountState.data
}

// hamletNonsenseWords are words that aren't in testdata/h.txt.
var hamletNonsenseWords = []string{
	// Edge cases.
	"",
	"\x00",
	"\xff",
	"`",
	"a\x00",
	"aaaaaa",
	"pol\x00nius",
	"youth\x00",
	"youti",
	"zzzzzz",
	// Capitalized versions of actual words in testdata/h.txt.
	"A",
	"Hamlet",
	"thEE",
	"YOUTH",
	// The following were generated by http://soybomb.com/tricks/words/
	"pectures",
	"exectly",
	"tricatrippian",
	"recens",
	"whiratroce",
	"troped",
	"balmous",
	"droppewry",
	"toilizing",
	"crocias",
	"eathrass",
	"cheakden",
	"speablett",
	"skirinies",
	"prefing",
	"bonufacision",
}

// buildHamletTestSST creates an sst file containing the hamlet word count data,
// using the given options.
func buildHamletTestSST(
	fs vfs.FS,
	filename string,
	compression Compression,
	fp FilterPolicy,
	ftype FilterType,
	comparer *Comparer,
	blockSize int,
	indexBlockSize int,
) error {
	wordCount := hamletWordCount()
	keys := wordCount.SortedKeys()

	// Write the key/value pairs to a new table, in increasing key order.
	f0, err := fs.Create(filename, vfs.WriteCategoryUnspecified)
	if err != nil {
		return err
	}

	writerOpts := WriterOptions{
		BlockSize:      blockSize,
		Comparer:       comparer,
		Compression:    compression,
		FilterPolicy:   fp,
		FilterType:     ftype,
		IndexBlockSize: indexBlockSize,
		MergerName:     "nullptr",
		TableFormat:    fixtureFormat,
	}

	w := NewWriter(objstorageprovider.NewFileWritable(f0), writerOpts)
	// NB: We don't have byte equality with RocksDB any longer.
	var rangeDelLength int
	var rangeDelCounter int
	var rangeDelStart []byte
	for i, k := range keys {
		v := wordCount[k]
		if err := w.Set([]byte(k), []byte(v)); err != nil {
			return err
		}
		// This mirrors the logic in `make-table.cc`. It adds range deletions of
		// increasing length for every 100 keys added.
		if i%100 == 0 {
			rangeDelStart = []byte(k)
			rangeDelCounter = 0
			rangeDelLength++
		}
		rangeDelCounter++

		if rangeDelCounter == rangeDelLength {
			if err := w.DeleteRange(rangeDelStart, []byte(k)); err != nil {
				return err
			}
		}
	}
	return w.Close()
}

// TestFixtureInfo contains all metadata necessary to generate a test sstable.
type TestFixtureInfo struct {
	Filename           string
	Compression        Compression
	FullKeyFilter      bool
	PrefixFilter       bool
	IndexBlockSize     int
	UseFixtureComparer bool
}

// TestFixtures contains all metadata necessary to generate the test SSTs.
var TestFixtures = []TestFixtureInfo{
	{
		Filename:           "h.sst",
		Compression:        SnappyCompression,
		FullKeyFilter:      false,
		PrefixFilter:       false,
		IndexBlockSize:     fixtureDefaultIndexBlockSize,
		UseFixtureComparer: false,
	},
	{
		Filename:           "h.no-compression.sst",
		Compression:        NoCompression,
		FullKeyFilter:      false,
		PrefixFilter:       false,
		IndexBlockSize:     fixtureDefaultIndexBlockSize,
		UseFixtureComparer: false,
	},
	{
		Filename:           "h.table-bloom.sst",
		Compression:        SnappyCompression,
		FullKeyFilter:      true,
		PrefixFilter:       false,
		IndexBlockSize:     fixtureDefaultIndexBlockSize,
		UseFixtureComparer: false,
	},
	{
		Filename:           "h.table-bloom.no-compression.sst",
		Compression:        NoCompression,
		FullKeyFilter:      true,
		PrefixFilter:       false,
		IndexBlockSize:     fixtureDefaultIndexBlockSize,
		UseFixtureComparer: false,
	},
	{
		Filename:           "h.table-bloom.no-compression.prefix_extractor.no_whole_key_filter.sst",
		Compression:        NoCompression,
		FullKeyFilter:      false,
		PrefixFilter:       true,
		IndexBlockSize:     fixtureDefaultIndexBlockSize,
		UseFixtureComparer: true,
	},
	{
		Filename:           "h.no-compression.two_level_index.sst",
		Compression:        NoCompression,
		FullKeyFilter:      false,
		PrefixFilter:       false,
		IndexBlockSize:     fixtureSmallIndexBlockSize,
		UseFixtureComparer: false,
	},
	{
		Filename:           "h.zstd-compression.sst",
		Compression:        ZstdCompression,
		FullKeyFilter:      false,
		PrefixFilter:       false,
		IndexBlockSize:     fixtureDefaultIndexBlockSize,
		UseFixtureComparer: false,
	},
}

// Build creates an sst file for the given fixture.
func (tf TestFixtureInfo) Build(fs vfs.FS, filename string) error {
	var fp base.FilterPolicy
	if tf.FullKeyFilter || tf.PrefixFilter {
		fp = bloom.FilterPolicy(10)
	}
	var comparer *Comparer
	if tf.UseFixtureComparer {
		comparer = fixtureComparer
	}

	return buildHamletTestSST(
		fs, filename, tf.Compression, fp, base.TableFilter,
		comparer,
		fixtureBlockSize,
		tf.IndexBlockSize,
	)
}

const fixtureDefaultIndexBlockSize = math.MaxInt32
const fixtureSmallIndexBlockSize = 128
const fixtureBlockSize = 2048
const fixtureFormat = TableFormatPebblev1

var fixtureComparer = func() *Comparer {
	c := *base.DefaultComparer
	// NB: this is named as such only to match the built-in RocksDB comparer.
	c.Name = "leveldb.BytewiseComparator"
	c.Split = func(a []byte) int {
		// TODO(tbg): It's difficult to provide a more meaningful prefix extractor
		// on the given dataset since it's not MVCC, and so it's impossible to come
		// up with a sensible one. We need to add a better dataset and use that
		// instead to get confidence that prefix extractors are working as intended.
		return len(a)
	}
	return &c
}()
