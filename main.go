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

var verboseFlag = cli.BoolFlag{
	Name: "verbose",
}

func main() {
	app := cli.NewApp()
	app.Name = "pho"
	app.Usage = "scraper for photo gallery 3 galleries"
	app.Version = "1.2.2"
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   "url",
			EnvVar: "PHOTO_GALLERY_URL",
			Usage:  "base url to the photo gallery",
		},
	}

	app.Commands = []cli.Command{
		{
			Name:   "ls",
			Usage:  "pho ls [path]",
			Action: ls,
			Flags:  []cli.Flag{recurseFlag, verboseFlag},
		},
		{
			Name:   "diff",
			Usage:  "pho diff [remote path] [local path]",
			Action: diff,
			Flags:  []cli.Flag{recurseFlag, verboseFlag},
		},
		{
			Name:   "fetch",
			Usage:  "pho fetch [remote path] [local path]",
			Action: fetch,
			Flags:  []cli.Flag{recurseFlag, verboseFlag},
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

	count := 0
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

				if verbose {
					log.Printf("Downloaded %v bytes for %s\n", n, localFile)
				}

				count++
				if count%100 == 0 {
					log.Printf("Downloaded %v images", count)
				}

			} else if err != nil {
				return err
			}

			return nil
		default:
			return fmt.Errorf("Unknown content type %v:%v", ct, node)
		}
	}

	err := walkPath(address, remotePath, recurse, 0, verbose, onIndex, onLeaf)
	if err != nil {
		log.Fatal(err)
	}

}

func diff(ctx *cli.Context) {
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
		location := path.Join(localPath, base, node)
		_, err := os.Stat(location)
		if os.IsNotExist(err) {
			fmt.Printf("%s\n", location)
		} else if err != nil {
			return err
		}

		return nil
	}

	err := walkPath(address, remotePath, recurse, 0, verbose, onIndex, doNothingOnLeaf)
	if err != nil {
		log.Fatal(err)
	}

}

func ls(ctx *cli.Context) {
	address := getAddress(ctx)
	recurse := ctx.Bool("recurse")
	verbose := ctx.Bool("verbose")

	var remotePath string
	if len(ctx.Args()) > 0 {
		remotePath = ctx.Args()[0]
	}

	onIndex := func(base string, node string, depth int) error {
		fmt.Println(strings.Repeat("\t", depth) + node)
		return nil
	}

	err := walkPath(address, remotePath, recurse, 0, verbose, onIndex, doNothingOnLeaf)
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

func walkPath(address string, base string, recurse bool, depth int, verbose bool, onIndex indexAction, onLeaf leafAction) error {
	remotePath := path.Join("/var/albums", base)
	resp, err := get(fmt.Sprintf("%s%s", address, remotePath))

	var ct string
	if err == nil {
		ct = getContentType(resp)
		switch ct {
		case "text/html":
			body := resp.Body
			nodes, err := getNodes(remotePath, body)
			body.Close()

			if err == nil {
				for _, node := range nodes {
					err = onIndex(base, node, depth)
					if err == nil && recurse {
						next := path.Join(base, node)
						err = handleWalkError(walkPath(address, next, recurse, depth+1, verbose, onIndex, onLeaf))
					}

					if err != nil {
						break
					}
				}
			}
		default:
			err = onLeaf(resp, base, ct)
			if err != nil {
				err = &leafError{err}
			}
		}
	}

	if err != nil {
		err = fmt.Errorf("%v %v: %v", resp.Request.URL, resp.Status, err)
	} else if verbose {
		log.Printf("Done with: %v %v %v", resp.Request.URL, resp.Status, ct)
	}

	return err
}

func handleWalkError(err error) error {
	if err != nil {
		_, is := err.(*leafError)
		if is {
			log.Printf("%v", err)
			err = nil
		}
	}

	return err
}

func getNodes(remotePath string, body io.ReadCloser) (nodes []string, err error) {
	tokenizer := html.NewTokenizer(body)
	for err == nil {
		tt := tokenizer.Next()
		err = tokenizer.Err()

		if err == nil && tt == html.StartTagToken {
			t := tokenizer.Token()
			isAnchor := t.Data == "a"
			if isAnchor {
				ok, node := getHref(t)
				if !ok {
					err = fmt.Errorf("No url for %v", t)
				}

				if !strings.Contains(remotePath, node) {
					nodes = append(nodes, node)
				}
			}

		}
	}

	if err == io.EOF {
		err = nil
	}
	return
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
