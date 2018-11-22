package engine

import (
	"html/template"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/blend/go-sdk/exception"
	"github.com/blend/go-sdk/logger"
	sdkTemplate "github.com/blend/go-sdk/template"

	"github.com/wcharczuk/photoblog/pkg/config"
	"github.com/wcharczuk/photoblog/pkg/constants"
	"github.com/wcharczuk/photoblog/pkg/model"
)

// New returns a new engine..
func New(cfg config.Config) Engine {
	return Engine{
		Config: cfg,
		Log:    logger.All().WithHeading("photoblog"),
	}
}

// Engine returns a
type Engine struct {
	Config config.Config
	Log    *logger.Logger
}

// CreateOutputPath creates the output path if it doesn't exist.
func (e Engine) CreateOutputPath() error {
	if _, err := os.Stat(e.Config.OutputOrDefault()); err != nil {
		return exception.New(MakeDir(e.Config.OutputOrDefault()))
	}
	return nil
}

// DiscoverPosts finds posts and returns an array of posts.
func (e Engine) DiscoverPosts() ([]model.Post, error) {
	imagesPath := e.Config.ImagesOrDefault()

	e.Log.SyncInfof("searching `%s` for images as posts", imagesPath)

	var posts []model.Post
	err := filepath.Walk(imagesPath, func(currentPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if currentPath == imagesPath {
			return nil
		}
		if info.IsDir() {
			e.Log.SyncInfof("reading `%s` as post", currentPath)
			post, err := e.ReadImage(currentPath)
			if err != nil {
				return err
			}
			posts = append(posts, *post)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return posts, nil
}

// ReadImage reads post metadata from a folder.
func (e Engine) ReadImage(path string) (*model.Post, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !stat.IsDir() {
		return nil, exception.New("not a directory").WithMessage(path)
	}

	// sniff image file
	// and metadata
	files, err := GetFileInfos(path)
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, exception.New("no child files found").WithMessage(path)
	}

	var post model.Post
	var modTime time.Time
	for _, fi := range files {
		name := fi.Name()
		if name == constants.DiscoveryFileMeta {
			if err := ReadYAML(filepath.Join(path, name), &post.Meta); err != nil {
				return nil, err
			}
		} else if HasExtension(name, constants.ImageExtensions...) && post.Image.IsZero() {
			post.Original = filepath.Join(path, name)
			post.File = name
			modTime = fi.ModTime()
			if err := ReadImage(post.Original, &post.Image); err != nil {
				return nil, err
			}
		}
	}

	if post.Meta.Posted.IsZero() {
		post.Meta.Posted = modTime
	}
	if post.Original == "" {
		return nil, exception.New("no images found").WithMessage(path)
	}
	return &post, nil
}

// ReadPartials reads all the partials named in the config.
func (e Engine) ReadPartials() ([]string, error) {
	var partials []string
	for _, partialPath := range e.Config.Layout.PartialsOrDefault() {
		contents, err := ioutil.ReadFile(partialPath)
		if err != nil {
			return nil, exception.New(err)
		}
		partials = append(partials, string(contents))
	}
	return partials, nil
}

// CompileTemplate compiles a template.
func (e Engine) CompileTemplate(templatePath string, partials []string) (*template.Template, error) {
	contents, err := ioutil.ReadFile(templatePath)
	if err != nil {
		return nil, exception.New(err)
	}

	vf := sdkTemplate.ViewFuncs{}.FuncMap()

	tmp := template.New("").Funcs(vf)
	for _, partial := range partials {
		_, err := tmp.Parse(partial)
		if err != nil {
			return nil, exception.New(err)
		}
	}

	final, err := tmp.Parse(string(contents))
	if err != nil {
		return nil, exception.New(err)
	}
	return final, nil
}

// Render writes the templates out for each of the posts.
func (e Engine) Render(posts ...model.Post) error {
	outputPath := e.Config.OutputOrDefault()

	partials, err := e.ReadPartials()
	if err != nil {
		return err
	}

	for _, pagePath := range e.Config.Layout.Pages {
		e.Log.Infof("rendering page %s", pagePath)

		page, err := e.CompileTemplate(pagePath, partials)
		if err != nil {
			return err
		}

		pageOutputPath := filepath.Join(outputPath, filepath.Base(pagePath))
		if err := e.WriteTemplate(page, pageOutputPath, ViewModel{Config: e.Config, Posts: posts}); err != nil {
			return err
		}
	}

	postTemplatePath := e.Config.Layout.PostOrDefault()
	postTemplate, err := e.CompileTemplate(postTemplatePath, partials)
	if err != nil {
		return err
	}

	// foreach post, render the post with single to <slug>/index.html
	for index, post := range posts {
		e.Log.Infof("rendering post %s", post.TitleOrDefault())
		slugPath := filepath.Join(outputPath, post.Slug())

		// make the slug directory tree (i.e. `mkdir -p <slug>`)
		if err := MakeDir(slugPath); err != nil {
			return exception.New(err)
		}

		var next, previous model.Post
		if index > 0 {
			previous = posts[index-1]
		}
		if index < len(posts)-1 {
			next = posts[index+1]
		}
		if err := e.WriteTemplate(postTemplate, filepath.Join(slugPath, constants.OutputFileIndex), ViewModel{
			Config:   e.Config,
			Post:     post,
			Previous: previous,
			Next:     next,
		}); err != nil {
			return err
		}

		if err := Copy(post.Original, filepath.Join(slugPath, filepath.Base(post.Original))); err != nil {
			return err
		}
	}

	return nil
}

func (e Engine) CopyAndResize(original, destination string) error {
	if err := Copy(post.Original, filepath.Join(slugPath, constants.ImageOriginal)); err != nil {
		return err
	}
}

// WriteTemplate writes a template to a given path with a given data viewmodel.
func (e Engine) WriteTemplate(tpl *template.Template, outputPath string, data interface{}) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := tpl.Execute(f, data); err != nil {
		return exception.New(err)
	}
	return nil
}

// CopyStatics copies static files to the output directory.
func (e Engine) CopyStatics() error {
	outputPath := e.Config.OutputOrDefault()

	// copy statics (things like css or js)
	staticPaths := e.Config.Layout.StaticsOrDefault()
	for _, staticPath := range staticPaths {
		if err := Copy(staticPath, outputPath); err != nil {
			return err
		}
	}
	return nil
}

// Generate generates the blog to the given output directory.
func (e Engine) Generate() error {

	// discover posts
	posts, err := e.DiscoverPosts()
	if err != nil {
		return err
	}

	e.Log.SyncInfof("discovered %d posts", len(posts))

	// create the output path if it doesn't exist
	if err := e.CreateOutputPath(); err != nil {
		return err
	}

	// render templates
	if err := e.Render(posts...); err != nil {
		return err
	}

	// copy statics.
	if err := e.CopyStatics(); err != nil {
		return err
	}

	return nil
}
