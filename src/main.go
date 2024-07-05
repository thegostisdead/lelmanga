package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

type MangaInfo struct {
	firstChapter string
	lastChapter  string
	title        string
	mainURL      string
}

var lastChapterRe = regexp.MustCompile(`(?m)\<span\sclass\=\"epcur\sepcurlast\"\>Chapitre\s(.*?)\<\/span\>`)
var titleRe = regexp.MustCompile(`(?m)\<h1\sclass\=\"entry\-title\"\sitemprop\=\"name\"\>(.*?)\<\/h1\>`)
var imagesRe = regexp.MustCompile(`(?m)\<script\>ts_reader\.run\((.*?)\)\;\<\/script\>`)

func extractRange(html string, info *MangaInfo) {
	info.firstChapter = "1"
	info.lastChapter = lastChapterRe.FindStringSubmatch(html)[1]
}

func extractTitle(html string, info *MangaInfo) {
	info.title = titleRe.FindStringSubmatch(html)[1]
}

func getMangaInfo(url string) MangaInfo {
	res, err := http.Get(url)
	if err != nil {
		fmt.Printf("error making http request: %s\n", err)
		os.Exit(1)
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		fmt.Print("Cannot get manga info.\n")
		fmt.Printf("error: status code %d\n", res.StatusCode)
		os.Exit(1)
	}

	info := MangaInfo{}
	info.mainURL = url

	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Println("error reading response body: ", err)
		os.Exit(1)
	}

	extractRange(string(body), &info)
	extractTitle(string(body), &info)

	fmt.Println("Title: ", info.title)
	fmt.Println("First Chapter: ", info.firstChapter)
	fmt.Println("Last Chapter: ", info.lastChapter)

	return info
}

func makeCbz(chapterDir string) {
	cmd := exec.Command("zip", "-r", "-j", fmt.Sprintf("%s.cbz", chapterDir), chapterDir)
	err := cmd.Run()
	if err != nil {
		fmt.Printf("error creating cbz: %s\n", err)
	}
}

func downloadChapter(url string, wg *sync.WaitGroup) {
	defer wg.Done()

	fmt.Println("Downloading chapter: ", url)
	res, err := http.Get(url)
	if err != nil {
		fmt.Printf("error making http request: %s\n", err)
		return
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		fmt.Printf("error: status code %d\n", res.StatusCode)
		return
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Println("error reading response body: ", err)
		return
	}
	jsonString := imagesRe.FindStringSubmatch(string(body))[1]

	var jsonData map[string]interface{}
	if err := json.Unmarshal([]byte(jsonString), &jsonData); err != nil {
		fmt.Println("error unmarshalling json: ", err)
		return
	}

	chapterDir := fmt.Sprintf("chapter_%s", filepath.Base(url))
	if err := os.MkdirAll(chapterDir, 0755); err != nil {
		fmt.Printf("error creating directory: %s\n", err)
		return
	}

	var extractImageURLs func(interface{}) []string
	extractImageURLs = func(data interface{}) []string {
		var urls []string
		switch v := data.(type) {
		case map[string]interface{}:
			if images, ok := v["images"].([]interface{}); ok {
				for _, img := range images {
					if imgURL, ok := img.(string); ok {
						urls = append(urls, imgURL)
					}
				}
			}
			for _, value := range v {
				urls = append(urls, extractImageURLs(value)...)
			}
		case []interface{}:
			for _, item := range v {
				urls = append(urls, extractImageURLs(item)...)
			}
		}
		return urls
	}

	imageURLs := extractImageURLs(jsonData)

	var imageWg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit to 5 concurrent image downloads

	for i, imgURL := range imageURLs {
		imageWg.Add(1)
		semaphore <- struct{}{}
		go func(i int, imgURL string) {
			defer imageWg.Done()
			defer func() { <-semaphore }()

			imgRes, err := http.Get(imgURL)
			if err != nil {
				fmt.Printf("error downloading image %d: %s\n", i, err)
				return
			}
			defer imgRes.Body.Close()

			imgFileName := filepath.Join(chapterDir, fmt.Sprintf("image_%d.jpg", i))
			imgFile, err := os.Create(imgFileName)
			if err != nil {
				fmt.Printf("error creating image file %d: %s\n", i, err)
				return
			}
			defer imgFile.Close()

			_, err = io.Copy(imgFile, imgRes.Body)
			if err != nil {
				fmt.Printf("error saving image %d: %s\n", i, err)
				return
			}

			fmt.Printf("Downloaded image %d\n", i)
		}(i, imgURL)
	}

	imageWg.Wait()
	makeCbz(chapterDir)
	fmt.Println("Chapter download complete")
}

func (manga *MangaInfo) downloadAllChapters() {
	start, err := strconv.Atoi(manga.firstChapter)
	if err != nil {
		fmt.Println("error converting first chapter to int: ", err)
		os.Exit(1)
	}

	end, err := strconv.Atoi(manga.lastChapter)
	if err != nil {
		fmt.Println("error converting last chapter to int: ", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // Limit to 3 concurrent chapter downloads

	for i := start; i <= end; i++ {
		wg.Add(1)
		semaphore <- struct{}{}
		go func(i int) {
			defer func() { <-semaphore }()
			fmt.Printf("Downloading chapter %d\n", i)
			path := strings.ReplaceAll(strings.ToLower(manga.title), " ", "-")
			url := fmt.Sprintf("https://www.lelmanga.com/%s-%d", path, i)
			downloadChapter(url, &wg)
		}(i)
	}

	wg.Wait()
}

func main() {

	runtime.GOMAXPROCS(4)
	fmt.Println("lelmanga.com Downloader")
	fmt.Println("Enter the URL of the manga you want to download")

	fmt.Println("URL: ", "https://www.lelmanga.com/manga/jujutsu-kaisen")

	manga := getMangaInfo("https://www.lelmanga.com/manga/jujutsu-kaisen")

	manga.downloadAllChapters()
}
