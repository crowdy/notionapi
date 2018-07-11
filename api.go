package notion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
)

const (
	notionHost = "https://www.notion.so"
	// modern Chrome
	userAgent  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_13_5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/69.0.3483.0 Safari/537.36"
	acceptLang = "en-US,en;q=0.9"
)

// HTTPInterceptor allows intercepting HTTP request so that a client
// of this library can provide e.g. a caching system for requests
// instead
type HTTPInterceptor interface {
	// OnRequest is called before http request is sent tot he server
	// If it returns non-nil response, it'll be used instead of sending
	// a request to the server
	OnReqeust(*http.Request) *http.Response
	// OnResponse is called after getting a response from the server
	// to allow e.g. caching of responses
	// Only called if the request was sent to the server (i.e. doesn't come
	// from OnRequest)
	OnResponse(*http.Response)
}

var (
	// HTTPIntercept allows intercepting http requests
	// e.g. to implement caching
	HTTPIntercept HTTPInterceptor
)

// PageInfo describes a single Notion page
type PageInfo struct {
	ID   string
	Page *Block
	// Users allows to find users that Page refers to by their ID
	Users *NotionUser
}

func doNotionAPI(apiURL string, requestData interface{}, parseFn func(d []byte) error) error {
	var js []byte
	var err error
	if requestData != nil {
		js, err = json.Marshal(requestData)
		if err != nil {
			return err
		}
	}
	uri := notionHost + apiURL
	body := bytes.NewBuffer(js)
	log("POST %s\n", uri)
	if len(js) > 0 {
		log("%s\n", string(js))
	}

	req, err := http.NewRequest("POST", uri, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", acceptLang)

	var rsp *http.Response
	if HTTPIntercept != nil {
		rsp = HTTPIntercept.OnReqeust(req)
	}

	realHTTPRequest := false
	if rsp == nil {
		realHTTPRequest = true
		rsp, err = http.DefaultClient.Do(req)
	}

	if err != nil {
		log("http.DefaultClient.Do() failed with %s\n", err)
		return err
	}
	if HTTPIntercept != nil && realHTTPRequest {
		HTTPIntercept.OnResponse(rsp)
	}
	if rsp.StatusCode != 200 {
		log("Error: status code %d\n", rsp.StatusCode)
		return fmt.Errorf("http.Post('%s') returned non-200 status code of %d", uri, rsp.StatusCode)
	}
	defer rsp.Body.Close()
	d, err := ioutil.ReadAll(rsp.Body)
	if err != nil {
		log("Error: ioutil.ReadAll() failed with %s\n", err)
		return err
	}
	logJSON(d)
	err = parseFn(d)
	if err != nil {
		log("Error: json.Unmarshal() failed with %s\n", err)
	}
	return err
}

func apiLoadPageChunk(pageID string, cur *cursor) (*loadPageChunkResponse, error) {
	// emulating notion's website api usage: 50 items on first request,
	// 30 on subsequent requests
	limit := 30
	apiURL := "/api/v3/loadPageChunk"
	if cur == nil {
		cur = &cursor{
			// to mimic browser api which sends empty array for this argment
			Stack: make([][]stack, 0),
		}
		limit = 50
	}
	req := &loadPageChunkRequest{
		PageID:          pageID,
		Limit:           limit,
		Cursor:          *cur,
		VerticalColumns: false,
	}
	var rsp *loadPageChunkResponse
	parse := func(d []byte) error {
		var err error
		rsp, err = parseLoadPageChunk(d)
		return err
	}
	err := doNotionAPI(apiURL, req, parse)
	if err != nil {
		return nil, err
	}
	return rsp, nil
}

func getFirstInlineBlock(v interface{}) (string, error) {
	inline, err := parseInlineBlocks(v)
	if err != nil {
		return "", err
	}
	if len(inline) > 0 {
		return inline[0].Text, nil
	}
	return "", nil
}

func parseProperties(block *Block) error {
	// has already parsed properties. Not sure if this can happen
	var err error
	if len(block.InlineContent) > 0 {
		return nil
	}
	props := block.Properties
	if title, ok := props["title"]; ok {
		block.InlineContent, err = parseInlineBlocks(title)
		if err != nil {
			return err
		}
		if block.Type == TypePage {
			if len(block.InlineContent) > 0 {
				block.Title = block.InlineContent[0].Text
			}
		}
	}
	if description, ok := props["description"]; ok {
		// for TypeBookmark
		block.Description, err = getFirstInlineBlock(description)
		if err != nil {
			return err
		}
	}

	if link, ok := props["link"]; ok {
		// for TypeBookmark
		block.Link, err = getFirstInlineBlock(link)
		if err != nil {
			return err
		}
	}

	if source, ok := props["source"]; ok {
		// for TypeBookmark, TypeImage
		block.Source, err = getFirstInlineBlock(source)
		if err != nil {
			return err
		}
		// TODO: for TypeImage convert block.Source to proxy via www.notion.so/image/${source}
		// https://www.notion.so/image/https%3A%2F%2Fs3-us-west-2.amazonaws.com%2Fsecure.notion-static.com%2F02c1738b-49c7-4a77-b69a-87a280ea759b%2Fthumbnail_ff8ef2e53954fcac40ddec79c761f4266d6ab5a5_EBOK_portrait.jpg?width=320
		// This is because some images in s3 are protected. also,
		// this supports resizing
	}

	// for TypeCode
	/*
		"language": [
			[
			  "Javascript"
			]
	*/
	return nil
}

func resolveBlocks(block *Block, blockMap map[string]*Block) error {
	err := parseProperties(block)
	if err != nil {
		return err
	}

	if TypeTodo == block.Type {
		if v, ok := block.Properties["checked"]; ok {
			if s, ok := v.(string); ok {
				block.IsChecked = strings.EqualFold(s, "Yes")
			}
		}
	}

	if block.Content != nil || len(block.ContentIDs) == 0 {
		return nil
	}
	n := len(block.ContentIDs)
	block.Content = make([]*Block, n, n)
	for i, id := range block.ContentIDs {
		resolved := blockMap[id]
		if resolved == nil {
			return fmt.Errorf("Couldn't resolve block with id '%s'", id)
		}
		block.Content[i] = resolved
		resolveBlocks(resolved, blockMap)
	}
	return nil
}

func apiGetRecordValues(ids []string) (*getRecordValuesResponse, error) {
	req := &getRecordValuesRequest{}

	for _, id := range ids {
		v := getRecordValuesRequestInner{
			Table: TableBlock,
			ID:    id,
		}
		req.Requests = append(req.Requests, v)
	}

	apiURL := "/api/v3/getRecordValues"
	var rsp *getRecordValuesResponse
	parse1 := func(d []byte) error {
		var err error
		rsp, err = parseGetRecordValues(d)
		return err
	}
	err := doNotionAPI(apiURL, req, parse1)
	if err != nil {
		return nil, err
	}
	return rsp, nil
}

func findMissingBlocks(startIds []string, idToBlock map[string]*Block) []string {
	var missing []string
	seen := map[string]struct{}{}
	toCheck := append([]string{}, startIds...)
	for len(toCheck) > 0 {
		id := toCheck[0]
		toCheck = toCheck[1:]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		block := idToBlock[id]
		if block == nil {
			missing = append(missing, id)
		} else {
			toCheck = append(toCheck, block.ContentIDs...)
		}
	}

	return missing
}

// GetPageInfo returns Noion page data given its id
func GetPageInfo(pageID string) (*PageInfo, error) {
	// TODO: validate pageID

	var pageInfo PageInfo
	{
		recVals, err := apiGetRecordValues([]string{pageID})
		if err != nil {
			return nil, err
		}
		pageID = recVals.Results[0].Value.ID
		pageInfo.ID = pageID
		pageInfo.Page = recVals.Results[0].Value
	}

	idToBlock := map[string]*Block{}
	for {
		rsp, err := apiLoadPageChunk(pageID, nil)
		if err != nil {
			return nil, err
		}
		for id, blockWithRole := range rsp.RecordMap.Blocks {
			idToBlock[id] = blockWithRole.Value
		}
		// TODO: handle stack
		break
	}

	// get blocks that are not already loaded, 30 per request
	missing := findMissingBlocks(pageInfo.Page.ContentIDs, idToBlock)
	//dbg("There are %d missing blocks, %#v\n", len(missing), missing)
	for len(missing) > 0 {
		// TODO: in smaller chunks
		recVals, err := apiGetRecordValues(missing)
		missing = nil
		if err != nil {
			return nil, err
		}
		for _, blockWithRole := range recVals.Results {
			block := blockWithRole.Value
			id := block.ID
			idToBlock[id] = block
		}
	}

	err := resolveBlocks(pageInfo.Page, idToBlock)
	if err != nil {
		return nil, err
	}
	return &pageInfo, nil
}