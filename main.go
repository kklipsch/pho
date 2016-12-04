package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/cenkalti/backoff"
	"github.com/urfave/cli"
)

var recurseFlag = cli.BoolFlag{
	Name:  "recurse,r",
	Usage: "will recursively walk the gallery",
}

func main() {
	app := cli.NewApp()
	app.Name = "pho"
	app.Usage = "scraper for photo gallery 3 galleries"
	app.Version = "1.2.1"
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   "url",
			EnvVar: "PHOTO_GALLERY_URL",
			Usage:  "base url to the photo gallery",
		},
		cli.BoolFlag{
			Name: "verbose",
		},
	}

	app.Commands = []cli.Command{
		{
			Name:   "ls",
			Usage:  "pho ls [path]",
			Action: ls,
			Flags:  []cli.Flag{recurseFlag},
		},
		{
			Name:   "diff",
			Usage:  "pho diff [remote path] [local path]",
			Action: diff,
			Flags:  []cli.Flag{recurseFlag},
		},
		{
			Name:   "fetch",
			Usage:  "pho fetch [remote path] [local path]",
			Action: fetch,
			Flags:  []cli.Flag{recurseFlag},
		},
	}

	app.Run(os.Args)
}

func fetch(ctx *cli.Context) {
	address := getAddress(ctx)
	recurse := ctx.Bool("recurse")
	verbose := ctx.Bool("verbose")

	remotePath := "/"
	if len(ctx.Args()) > 0 {
		remotePath = ctx.Args()[0]
	}

	localPath := "."
	if len(ctx.Args()) > 1 {
		localPath = ctx.Args()[1]
	}

	onIndex := func(base string, node string, depth int) error {
		if verbose {
			log.Printf("Traversing %v", node)
		}

		return nil
	}

	onLeaf := func(resp *http.Response, node string, ct string) error {
		switch ct {
		case "image/png":
			fallthrough
		case "image/jpeg":
			folder := path.Join(localPath, path.Dir(node))
			file := path.Base(node)
			localFile := path.Join(folder, file)
			_, err := os.Stat(localFile)
			if os.IsNotExist(err) {
				err = os.MkdirAll(folder, os.ModePerm)
				if err != nil {
					return err
				}

				output, err := os.Create(localFile)
				if err != nil {
					return err
				}

				defer output.Close()
				defer resp.Body.Close()
				n, err := io.Copy(output, resp.Body)
				if err != nil {
					return err
				}

				fmt.Printf("Downloaded %v bytes for %s\n", n, localFile)
			} else if err != nil {
				return err
			}

			return nil
		default:
			return fmt.Errorf("Unknown content type %v:%v", ct, node)
		}
	}

	err := walkPath(address, remotePath, recurse, 0, onIndex, onLeaf)
	if err != nil {
		log.Fatal(err)
	}

}

func diff(ctx *cli.Context) {
	address := getAddress(ctx)
	recurse := ctx.Bool("recurse")

	remotePath := "/"
	if len(ctx.Args()) > 0 {
		remotePath = ctx.Args()[0]
	}

	localPath := "."
	if len(ctx.Args()) > 1 {
		localPath = ctx.Args()[1]
	}

	onIndex := func(base string, node string, depth int) error {
		location := path.Join(localPath, base, node)
		_, err := os.Stat(location)
		if os.IsNotExist(err) {
			fmt.Printf("%s\n", location)
		} else if err != nil {
			return err
		}

		return nil
	}

	err := walkPath(address, remotePath, recurse, 0, onIndex, doNothingOnLeaf)
	if err != nil {
		log.Fatal(err)
	}

}

func ls(ctx *cli.Context) {
	address := getAddress(ctx)
	recurse := ctx.Bool("recurse")

	var remotePath string
	if len(ctx.Args()) > 0 {
		remotePath = ctx.Args()[0]
	}

	onIndex := func(base string, node string, depth int) error {
		fmt.Println(strings.Repeat("\t", depth) + node)
		return nil
	}

	err := walkPath(address, remotePath, recurse, 0, onIndex, doNothingOnLeaf)
	if err != nil {
		log.Fatal(err)
	}
}

type indexAction func(base string, node string, depth int) error
type leafAction func(resp *http.Response, path string, contentType string) error

func doNothingOnLeaf(resp *http.Response, path string, contentType string) error {
	return nil
}

type leafError struct {
	inner error
}

func (l *leafError) Error() string {
	return fmt.Sprintf("Leaf error: %v", l.inner)
}

func walkPath(address string, base string, recurse bool, depth int, onIndex indexAction, onLeaf leafAction) error {
	remotePath := path.Join("/var/albums", base)
	resp, err := get(fmt.Sprintf("%s%s", address, remotePath))
	if err != nil {
		return err
	}

	ct := getContentType(resp)
	switch ct {
	case "text/html":
		body := resp.Body
		defer body.Close()

		tokenizer := html.NewTokenizer(body)
		for {
			tt := tokenizer.Next()
			switch {
			case tt == html.ErrorToken:
				// End of the document, we're done
				return nil
			case tt == html.StartTagToken:
				t := tokenizer.Token()
				isAnchor := t.Data == "a"
				if !isAnchor {
					continue
				}

				ok, node := getHref(t)
				if !ok {
					return fmt.Errorf("No url for %v", t)
				}

				if strings.Contains(remotePath, node) {
					continue
				}

				next := path.Join(base, node)

				err := onIndex(base, node, depth)
				if err != nil {
					return fmt.Errorf("Index error: %v", err)
				}

				if recurse {
					err = walkPath(address, next, recurse, depth+1, onIndex, onLeaf)
					if err != nil {
						switch err.(type) {
						case *leafError:
							log.Printf("%v", err)
						default:
							return err
						}
					}
				}
			}
		}
	default:
		err = onLeaf(resp, base, ct)
		if err != nil {
			return &leafError{err}
		}
	}

	return nil
}
func getContentType(resp *http.Response) string {
	parts := strings.Split(resp.Header.Get("Content-Type"), ";")
	return parts[0]
}

func getAddress(ctx *cli.Context) string {
	address := ctx.GlobalString("url")
	if address == "" {
		log.Fatal("host or must be provided")
	}

	return address
}

func getHref(t html.Token) (ok bool, href string) {
	for _, a := range t.Attr {
		if a.Key == "href" {
			href = a.Val
			ok = true
		}
	}

	return
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

func get(url string) (resp *http.Response, err error) {
	op := func() error {
		var e error
		resp, e = httpClient.Get(url)
		return e
	}

	notify := func(err error, t time.Duration) {
		log.Printf("%v waiting %v to retry...\n", err, t)
	}

	err = backoff.RetryNotify(op, backoff.NewExponentialBackOff(), notify)

	return
}
