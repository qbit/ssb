// SPDX-License-Identifier: MIT

package sbot

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"go.cryptoscope.co/luigi"
	"go.cryptoscope.co/margaret"
	refs "go.mindeco.de/ssb-refs"

	"go.cryptoscope.co/ssb/indexes"
	"go.cryptoscope.co/ssb/multilogs"
	"go.cryptoscope.co/ssb/repo"
)

// NullFeed overwrites all the entries from ref in repo with zeros
func (s *Sbot) NullFeed(ref *refs.FeedRef) error {
	ctx := context.Background()

	uf, ok := s.GetMultiLog(multilogs.IndexNameFeeds)
	if !ok {
		return errors.Errorf("NullFeed: failed to open multilog")
	}

	feedAddr := ref.StoredAddr()
	userSeqs, err := uf.Get(feedAddr)
	if err != nil {
		return errors.Wrap(err, "NullFeed: failed to open log for feed argument")
	}

	src, err := userSeqs.Query()
	if err != nil {
		return errors.Wrap(err, "NullFeed: failed create user seqs query")
	}

	for {
		v, err := src.Next(ctx)
		if err != nil {
			if luigi.IsEOS(err) {
				break
			}
			return err
		}
		seq, ok := v.(margaret.Seq)
		if !ok {
			return errors.Errorf("NullFeed: not a sequence from userlog query")
		}
		err = s.RootLog.Null(seq)
		if err != nil {
			return err
		}
	}

	err = uf.Delete(feedAddr)
	if err != nil {
		return errors.Wrapf(err, "NullFeed: error while deleting feed from userFeeds index")
	}

	err = s.GraphBuilder.DeleteAuthor(ref)
	if err != nil {
		return errors.Wrapf(err, "NullFeed: error while deleting feed from graph index")
	}

	return nil
}

// Drop indicies deletes the following folders of the indexes.
// TODO: check that sbot isn't running?
func DropIndicies(r repo.Interface) error {

	// drop indicies
	var mlogs = []string{
		multilogs.IndexNameFeeds,
		// multilogs.IndexNameTypes,
		multilogs.IndexNamePrivates,
	}
	for _, i := range mlogs {
		dbPath := r.GetPath(repo.PrefixMultiLog, i)
		err := os.RemoveAll(dbPath)
		if err != nil {
			err = errors.Wrapf(err, "mkdir error for %q", dbPath)
			return err
		}
	}
	var badger = []string{
		indexes.FolderNameContacts,
	}
	for _, i := range badger {
		dbPath := r.GetPath(repo.PrefixIndex, i)
		err := os.RemoveAll(dbPath)
		if err != nil {
			err = errors.Wrapf(err, "mkdir error for %q", dbPath)
			return err
		}
	}
	log.Println("removed index folders")
	return nil
}

func RebuildIndicies(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		err = errors.Wrap(err, "RebuildIndicies: failed to open sbot")
		return err
	}

	if !fi.IsDir() {
		return errors.Errorf("RebuildIndicies: repo path is not a directory")
	}

	// rebuilding indexes
	sbot, err := New(
		DisableNetworkNode(),
		WithRepoPath(path),
		DisableLiveIndexMode(),
	)
	if err != nil {
		err = errors.Wrap(err, "failed to open sbot")
		return err
	}

	// TODO: not sure if I should hook this signal here..
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		sbot.Shutdown()

		time.Sleep(5 * time.Second)

		err := sbot.Close()
		log.Println("sbot closed:", err)

		time.Sleep(5 * time.Second)
		os.Exit(0)
	}()

	start := time.Now()
	log.Println("started sbot for re-indexing")
	err = sbot.Close()
	log.Println("re-indexing took:", time.Since(start))
	return errors.Wrap(err, "RebuildIndicies: failed to close sbot")
}
