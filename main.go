package main

import (
	"errors"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"quiethn.hopertz.me/hn"
)

func main() {
	// parse flags
	var port, numStories int
	flag.IntVar(&port, "port", 3000, "the port to start the web server on")
	flag.IntVar(&numStories, "num_stories", 30, "the number of top stories to display")
	flag.Parse()

	tpl := template.Must(template.ParseFiles("./index.gohtml"))

	http.HandleFunc("/", handler(numStories, tpl))

	// Start the server
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

func handler(numStories int, tpl *template.Template) http.HandlerFunc {

	sc := storyCache{
		numStories: numStories,
		duration:   6 * time.Second,
	}

	// The greates magic trick
	// Before even the server starts fetch stories and put in cache
	// Update Cache after every 3 seconds
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		for {

			temp := storyCache{
				numStories: numStories,
				duration:   6 * time.Second,
			}

			temp.stories()
			sc.mutex.Lock()
			sc.cache = temp.cache
			sc.expiration = temp.expiration
			sc.mutex.Unlock()

			<-ticker.C
		}
	}()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		stories, err := sc.stories()

		if err != nil {
			http.Error(w, "Failed to process the template", http.StatusInternalServerError)
			return

		}
		data := templateData{
			Stories: stories,
			Time:    time.Since(start),
		}
		err = tpl.Execute(w, data)
		if err != nil {
			http.Error(w, "Failed to process the template", http.StatusInternalServerError)
			return
		}
	})
}

func isStoryLink(item item) bool {
	return item.Type == "story" && item.URL != ""
}

func parseHNItem(hnItem hn.Item) item {
	ret := item{Item: hnItem}
	url, err := url.Parse(ret.URL)
	if err == nil {
		ret.Host = strings.TrimPrefix(url.Hostname(), "www.")
	}
	return ret
}

// item is the same as the hn.Item, but adds the Host field
type item struct {
	hn.Item
	Host string
}

type templateData struct {
	Stories []item
	Time    time.Duration
}

type storyCache struct {
	numStories int
	cache      []item
	expiration time.Time
	duration   time.Duration
	mutex      sync.Mutex
}

// Checks if there is a story cache within the expiration time
// If there is no cache Go and fetch the stories.
func (sc *storyCache) stories() ([]item, error) {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()
	if time.Since(sc.expiration) < 0 {
		return sc.cache, nil

	}
	stories, err := getTopStories(sc.numStories)
	if err != nil {
		return nil, err
	}

	// Update cache  with new stories
	sc.cache = stories

	//Update expiration Time
	sc.expiration = time.Now().Add(sc.duration)

	return sc.cache, nil
}

func getTopStories(numStories int) ([]item, error) {
	var client hn.Client
	ids, err := client.TopItems()

	if err != nil {
		return nil, errors.New("failed to load top stories")
	}

	var stories []item

	at := 0

	for len(stories) < numStories {

		need := (numStories - len(stories)) * 5 / 4
		fmt.Println(numStories, len(stories), at, need)
		stories = append(stories, getStories(ids[at:at+need])...)
		at += need

	}

	return stories[0:30], nil

}

// Most of the magic happens here
func getStories(ids []int) []item {

	type result struct {
		idx  int
		item item
		err  error
	}
	var resultCh = make(chan result)

	for i := 0; i < len(ids); i++ {
		// Fetch stories using groutine
		go func(idx, id int) {
			var client hn.Client
			hnItem, err := client.GetItem(id)
			if err != nil {
				resultCh <- result{idx: idx, err: err}

			}
			resultCh <- result{idx: idx, item: parseHNItem(hnItem)}
		}(i, ids[i])
	}

	var results []result

	// Receive the stories result
	for i := 0; i < len(ids); i++ {
		results = append(results, <-resultCh)
	}

	// Sort the result of the slice
	sort.Slice(results, func(i int, j int) bool {
		return results[i].idx < results[j].idx
	})
	var stories []item

	for _, res := range results {
		// Skip the errors
		if res.err != nil {
			continue

		}

		// Condition for append
		if isStoryLink(res.item) {
			stories = append(stories, res.item)

		}
	}

	return stories

}
