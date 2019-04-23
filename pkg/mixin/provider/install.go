package mixinprovider

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"runtime"

	"github.com/deislabs/porter/pkg/mixin/feed"
	"github.com/deislabs/porter/pkg/mixin"
	"github.com/pkg/errors"
)

func (p *FileSystem) Install(opts mixin.InstallOptions) (mixin.Metadata, error) {
	if opts.FeedURL != "" {
		return p.InstallFromFeedURL(opts)
	}

	return p.InstallFromURL(opts)
}

func (p *FileSystem) InstallFromURL(opts mixin.InstallOptions) (mixin.Metadata, error) {
	clientUrl := opts.GetParsedURL()
	clientUrl.Path = path.Join(clientUrl.Path, opts.Version, fmt.Sprintf("%s-%s-%s%s", opts.Name, runtime.GOOS, runtime.GOARCH, mixin.FileExt))

	runtimeUrl := opts.GetParsedURL()
	runtimeUrl.Path = path.Join(runtimeUrl.Path, opts.Version, fmt.Sprintf("%s-linux-amd64", opts.Name))

	return p.downloadMixin(opts.Name, clientUrl, runtimeUrl)
}

func (p *FileSystem) InstallFromFeedURL(opts mixin.InstallOptions) (mixin.Metadata, error) {
	feedUrl := opts.GetParsedFeedURL()
	tmpDir, err := p.FileSystem.TempDir("porter", "")
	if err != nil {
		return mixin.Metadata{}, errors.Wrap(err, "error creating temp directory")
	}
	defer p.FileSystem.RemoveAll(tmpDir)
	feedPath := filepath.Join(tmpDir, "atom.xml")

	err = p.downloadFile(feedUrl, feedPath)
	if err != nil {
		return mixin.Metadata{}, err
	}

	searchFeed := feed.MixinFeed{}
	err = searchFeed.Load(feedPath, p.Context)
	if err != nil {
		return mixin.Metadata{}, err
	}

	result := searchFeed.Search(opts.Name, opts.Version)
	if result == nil {
		return mixin.Metadata{}, errors.Errorf("the mixin feed at %s does not contain an entry for %s @ %s", opts.FeedURL, opts.Name, opts.Version)
	}

	clientUrl := result.FindDownloadURL(runtime.GOOS, runtime.GOARCH)
	if clientUrl == nil {
		return mixin.Metadata{}, errors.Errorf("%s @ %s did not publish a download for %s/%s", opts.Name, opts.Version, runtime.GOOS, runtime.GOARCH)
	}

	runtimeUrl := result.FindDownloadURL("linux", "amd64")
	if runtimeUrl == nil {
		return mixin.Metadata{}, errors.Errorf("%s @ %s did not publish a download for linux/amd64", opts.Name, opts.Version)
	}

	return p.downloadMixin(opts.Name, *clientUrl, *runtimeUrl)
}

func (p *FileSystem) downloadMixin(name string, clientUrl url.URL, runtimeUrl url.URL) (mixin.Metadata, error) {
	mixinsDir, err := p.GetMixinsDir()
	if err != nil {
		return mixin.Metadata{}, err
	}
	mixinDir := filepath.Join(mixinsDir, name)

	clientPath := filepath.Join(mixinDir, name) + mixin.FileExt
	err = p.downloadFile(clientUrl, clientPath)
	if err != nil {
		return mixin.Metadata{}, err
	}

	runtimePath := filepath.Join(mixinDir, name+"-runtime")
	err = p.downloadFile(runtimeUrl, runtimePath)
	if err != nil {
		p.FileSystem.RemoveAll(mixinDir) // If the runtime download fails, cleanup the mixin so it's not half installed
		return mixin.Metadata{}, err
	}

	m := mixin.Metadata{
		Name:       name,
		Dir:        mixinDir,
		ClientPath: clientPath,
	}
	return m, nil
}

func (p *FileSystem) downloadFile(url url.URL, destPath string) error {
	if p.Debug {
		fmt.Fprintf(p.Err, "Downloading %s to %s\n", url.String(), destPath)
	}

	resp, err := http.Get(url.String())
	if err != nil {
		return errors.Wrapf(err, "error downloading the mixin from %s", url.String())
	}
	if resp.StatusCode != 200 {
		return errors.Errorf("bad status returned when downloading the mixin from %s (%d)", url.String(), resp.StatusCode)
	}
	defer resp.Body.Close()

	// Ensure the parent directories exist
	parentDir := filepath.Dir(destPath)
	err = p.FileSystem.MkdirAll(parentDir, 0755)
	if err != nil {
		errors.Wrapf(err, "unable to create parent directory %s", parentDir)
	}
	cleanup := func() {
		p.FileSystem.RemoveAll(parentDir) // If we can't install the mixin, don't leave traces of it
	}

	destFile, err := p.FileSystem.Create(destPath)
	if err != nil {
		cleanup()
		return errors.Wrapf(err, "could not create the mixin at %s", destPath)
	}
	defer destFile.Close()
	err = p.FileSystem.Chmod(destPath, 0755)
	if err != nil {
		cleanup()
		return errors.Wrapf(err, "could not set the mixin as executable at %s", destPath)
	}

	_, err = io.Copy(destFile, resp.Body)
	if err != nil {
		cleanup()
		return errors.Wrapf(err, "error writing the mixin to %s", destPath)
	}
	return nil
}