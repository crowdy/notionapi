package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/kjk/notion"
)

func openLogFileForPageID(pageID string) (io.WriteCloser, error) {
	name := fmt.Sprintf("%s.go.log.txt", pageID)
	path := filepath.Join("log", name)
	f, err := os.Create(path)
	if err != nil {
		fmt.Printf("os.Create('%s') failed with %s\n", path, err)
		return nil, err
	}
	notion.Logger = f
	return f, nil
}

func genHTMLTitle(f io.Writer, pageBlock *notion.Block) {
	title := ""
	if len(pageBlock.InlineContent) > 0 {
		title = pageBlock.InlineContent[0].Text
		title = template.HTMLEscapeString(title)
	}

	s := fmt.Sprintf(`  <div class="title">%s</div>%s`, title, "\n")
	io.WriteString(f, s)
}

func genInlineBlockHTML(f io.Writer, b *notion.InlineBlock) error {
	var start, close string
	if b.AttrFlags&notion.AttrBold != 0 {
		start += "<b>"
		close += "</b>"
	}
	if b.AttrFlags&notion.AttrItalic != 0 {
		start += "<i>"
		close += "</i>"
	}
	if b.AttrFlags&notion.AttrStrikeThrought != 0 {
		start += "<strike>"
		close += "</strike>"
	}
	if b.AttrFlags&notion.AttrCode != 0 {
		start += "<code>"
		close += "</code>"
	}
	skipText := false
	for _, attrRaw := range b.Attrs {
		switch attr := attrRaw.(type) {
		case *notion.AttrLink:
			start += fmt.Sprintf(`<a href="%s">%s</a>`, attr.Link, b.Text)
			skipText = true
		case *notion.AttrUser:
			start += fmt.Sprintf(`<span class="user">@%s</span>`, attr.UserID)
			skipText = true
		case *notion.AttrDate:
			// TODO: serialize date properly
			start += fmt.Sprintf(`<span class="date">@TODO: date</span>`)
			skipText = true
		}
	}
	if !skipText {
		start += b.Text
	}
	_, err := io.WriteString(f, start+close)
	if err != nil {
		return err
	}
	return nil
}

func genInlineBlocksHTML(f io.Writer, blocks []*notion.InlineBlock) error {
	for _, block := range blocks {
		err := genInlineBlockHTML(f, block)
		if err != nil {
			return err
		}
	}
	return nil
}

func genBlockSurroudedHTML(f io.Writer, block *notion.Block, start, close string, level int) {
	io.WriteString(f, start+"\n")
	genInlineBlocksHTML(f, block.InlineContent)
	genBlocksHTML(f, block.Content, level+1)
	io.WriteString(f, close+"\n")
}

func genBlockHTML(f io.Writer, block *notion.Block, level int) {
	levelCls := ""
	if level > 0 {
		levelCls = fmt.Sprintf(" lvl%d", level)
	}

	switch block.Type {
	case notion.TypeText:
		start := fmt.Sprintf(`<div class="text%s">`, levelCls)
		close := `</div>`
		genBlockSurroudedHTML(f, block, start, close, level)
	case notion.TypeHeader:
		start := fmt.Sprintf(`<h1 class="hdr%s">`, levelCls)
		close := `</h1>`
		genBlockSurroudedHTML(f, block, start, close, level)
	case notion.TypeSubHeader:
		start := fmt.Sprintf(`<h2 class="hdr%s">`, levelCls)
		close := `</h2>`
		genBlockSurroudedHTML(f, block, start, close, level)
	case notion.TypeTodo:
		// TODO: add checked
		clsChecked := ""
		if block.IsChecked {
			clsChecked = " is_checked"
		}
		start := fmt.Sprintf(`<div class="todo%s%s">`, levelCls, clsChecked)
		close := `</div>`
		genBlockSurroudedHTML(f, block, start, close, level)
	case notion.TypeToggle:
		start := fmt.Sprintf(`<div class="toggle%s">`, levelCls)
		close := `</div>`
		genBlockSurroudedHTML(f, block, start, close, level)
	case notion.TypeBulletedList:
		start := fmt.Sprintf(`<div class="bullet_list%s">`, levelCls)
		close := `</div>`
		genBlockSurroudedHTML(f, block, start, close, level)
	case notion.TypeNumberedList:
		start := fmt.Sprintf(`<div class="numbered_list%s">`, levelCls)
		close := `</div>`
		genBlockSurroudedHTML(f, block, start, close, level)
	case notion.TypeQuote:
		start := fmt.Sprintf(`<quote class="%s">`, levelCls)
		close := `</quote>`
		genBlockSurroudedHTML(f, block, start, close, level)
	case notion.TypeDivider:
		fmt.Fprintf(f, `<hr class="%s"/>`+"\n", levelCls)
	case notion.TypePage:
		id := strings.TrimSpace(block.ID)
		cls := "page"
		if block.IsLinkToPage() {
			cls = "page_link"
		}
		title := template.HTMLEscapeString(block.Title)
		url := normalizeID(id) + ".html"
		html := fmt.Sprintf(`<div class="%s%s"><a href="%s">%s</a></div>`, cls, levelCls, url, title)
		fmt.Fprintf(f, "%s\n", html)
	case notion.TypeCode:
		code := template.HTMLEscapeString(block.Code)
		fmt.Fprintf(f, `<div class="%s">Lang for code: %s</div>
<pre class="%s">
%s
</pre>`, levelCls, block.CodeLanguage, levelCls, code)
	case notion.TypeBookmark:
		fmt.Fprintf(f, `<div class="bookmark %s">Bookmark to %s</div>`+"\n", levelCls, block.Link)
	case notion.TypeGist:
		fmt.Fprintf(f, `<div class="gist %s">Gist for %s</div>`+"\n", levelCls, block.Source)
	case notion.TypeImage:
		link := block.Source
		fmt.Fprintf(f, `<img class="%s" src="%s" />`+"\n", levelCls, link)
	case notion.TypeCollectionView:
		// TODO: implement me
	default:
		fmt.Printf("Unsupported block type '%s', id: %s\n", block.Type, block.ID)
		panic(fmt.Sprintf("Unsupported block type '%s'", block.Type))
	}
}

// convert 2131b10c-ebf6-4938-a127-7089ff02dbe4 to 2131b10cebf64938a1277089ff02dbe4
func normalizeID(s string) string {
	return strings.Replace(s, "-", "", -1)
}

func genBlocksHTML(f io.Writer, blocks []*notion.Block, level int) {
	for _, block := range blocks {
		genBlockHTML(f, block, level)
	}
}

func genHTML(pageID string, pageInfo *notion.PageInfo) []byte {
	f := &bytes.Buffer{}
	title := pageInfo.Page.Title
	title = template.HTMLEscapeString(title)
	fmt.Fprintf(f, `<!doctype html>
<html>
	<head>
		<meta http-equiv="Content-Type" content="text/html; charset=utf-8" />
		<meta name="viewport" content="width=device-width, initial-scale=1">
		<link href="/main.css" rel="stylesheet">
		<title>%s</title>
	</head>
	<body>`, title)

	page := pageInfo.Page
	genHTMLTitle(f, page)
	genBlocksHTML(f, page.Content, 0)
	fmt.Fprintf(f, "</body>\n</html>\n")
	return f.Bytes()
}

func getPageInfoCached(pageID string) (*notion.PageInfo, error) {
	var pageInfo notion.PageInfo
	cachedPath := filepath.Join("cache", pageID+".json")
	if useCache {
		d, err := ioutil.ReadFile(cachedPath)
		if err == nil {
			err = json.Unmarshal(d, &pageInfo)
			if err == nil {
				fmt.Printf("Got data for pageID %s from cache file %s\n", pageID, cachedPath)
				return &pageInfo, nil
			}
			// not a fatal error, just a warning
			fmt.Printf("json.Unmarshal() on '%s' failed with %s\n", cachedPath, err)
		}
	}
	res, err := notion.GetPageInfo(pageID)
	if err != nil {
		return nil, err
	}
	d, err := json.Marshal(res)
	if err == nil {
		err = ioutil.WriteFile(cachedPath, d, 0644)
		if err != nil {
			// not a fatal error, just a warning
			fmt.Printf("ioutil.WriteFile(%s) failed with %s\n", cachedPath, err)
		}
	} else {
		// not a fatal error, just a warning
		fmt.Printf("json.Marshal() on pageID '%s' failed with %s\n", pageID, err)
	}
	return res, nil
}

func toHTML(pageID, path string) (*notion.PageInfo, error) {
	fmt.Printf("toHTML: pageID=%s, path=%s\n", pageID, path)
	lf, _ := openLogFileForPageID(pageID)
	if lf != nil {
		defer lf.Close()
	}
	pageInfo, err := getPageInfoCached(pageID)
	if err != nil {
		fmt.Printf("GetPageInfo('%s') failed with %s\n", pageID, err)
		return nil, err
	}
	d := genHTML(pageID, pageInfo)
	err = ioutil.WriteFile(path, d, 0644)
	return pageInfo, err
}

func findSubPageIDs(blocks []*notion.Block) []string {
	var res []string
	for _, block := range blocks {
		if block.Type == notion.TypePage {
			res = append(res, block.ID)
		}
	}
	return res
}

var (
	flgRecursive bool
	flgNoCache   bool
	useCache     bool // !flgNoCache, reads better
	toVisit      []string
)

func usageAndExit() {
	cmd := filepath.Base(os.Args[0])
	fmt.Printf(`Usage:
%s [-recursive] [-no-cache] urlOrId ...
urlOrId can be a Notion page URL like:
https://www.notion.so/kjkpublic/Test-page-text-4c6a54c68b3e4ea2af9cfaabcc88d58d
or a notion page id:
4c6a54c68b3e4ea2af9cfaabcc88d58d
Pages must be publicly visible.
`, cmd)
	os.Exit(1)
}

func parseCmdFlags() {
	flag.BoolVar(&flgRecursive, "recursive", false, "if true, recursively download page")
	flag.BoolVar(&flgNoCache, "no-cache", false, "if true, use cached responses, if available from previous runs")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Printf("Must provide Notion public page url or id\n")
		usageAndExit()
	}

	useCache = !flgNoCache

	// handle:
	// https://www.notion.so/kjkpublic/Test-page-c969c9455d7c4dd79c7f860f3ace6429
	// or
	// 300db9dc27c84958a08b8d0c37f4cfe5
	for _, arg := range args {
		parts := strings.Split(arg, "-")
		n := len(parts)
		id := parts[0]
		if n > 1 {
			id = parts[n-1]
		}
		id = normalizeID(id)
		if len(id) != 32 {
			fmt.Printf("Id '%s' extracted from '%s' doesn't look like a valid Notion page id\n", id, arg)
			usageAndExit()
		}
		toVisit = append(toVisit, id)
	}
}

func copyFile(dst, src string) error {
	d, err := ioutil.ReadFile(src)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(dst, d, 0644)
}

func copyCSS() {
	src := filepath.Join("cmd", "tohatml", "main.css")
	dst := filepath.Join("www", "main.css")
	copyFile(dst, src)
}

func main() {
	parseCmdFlags()

	os.MkdirAll("log", 755)
	os.MkdirAll("cache", 755)
	os.MkdirAll("www", 755)

	seen := map[string]struct{}{}
	firstPage := true
	for len(toVisit) > 0 {
		pageID := toVisit[0]
		toVisit = toVisit[1:]
		id := normalizeID(pageID)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		name := id + ".html"
		if firstPage {
			name = "index.html"
		}
		path := filepath.Join("www", name)
		pageInfo, err := toHTML(id, path)
		if err != nil {
			fmt.Printf("toHTML('%s') failed with %s\n", id, err)
		}
		if flgRecursive {
			subPages := findSubPageIDs(pageInfo.Page.Content)
			toVisit = append(toVisit, subPages...)
		}
		firstPage = false
	}
	copyCSS()
}
