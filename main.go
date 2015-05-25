package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/txchen/tlog"
)

type imageSet struct {
	Imgs []string `json:"imgs"`
}

type acgResult struct {
	Data []imageSet `json:"data"`
}

type downloadResult struct {
	downloaded bool
	data       []byte
	err        error
	url        string
	h          bool
}

const imageBaseURL = "http://acg.sugling.in/_uploadfiles/iphone5/640/"
const maxRetries = 3

var fnRegex = regexp.MustCompile("\\d{6}\\d+\\.jpg")

func downloadImage(url string, h bool) downloadResult {
	result := downloadResult{url: url, h: h}
	if !fnRegex.MatchString(url) {
		result.err = fmt.Errorf("image url format unexpected: %v", url)
		return result
	}

	response, err := http.Get(imageBaseURL + url)
	defer response.Body.Close()
	if err != nil {
		result.err = fmt.Errorf("error download image from http: %v , %v", url, err)
		return result
	}

	result.data, err = ioutil.ReadAll(response.Body)
	result.downloaded = true
	return result
}

func retryDownloadImage(url string, h bool) downloadResult {
	result := downloadResult{}
	for i := 0; i < maxRetries; i++ {
		result = downloadImage(url, h)
		if result.err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	return result
}

func saveImage(result downloadResult) (downloaded string) {
	if !result.downloaded {
		tlog.ERROR.Printf("image not downloaded: %v, %v", result.url, result.err)
		return
	}

	// check if the file is already there
	imageDir := "20" + result.url[0:2] + "/" + result.url[2:4] + "/" + result.url[4:6] + "/"
	if result.h {
		imageDir = "images/H/" + imageDir
	} else {
		imageDir = "images/NH/" + imageDir
	}
	imageFileName := imageDir + result.url
	tempImgFileName := imageFileName + ".tmp"

	if err := os.MkdirAll(imageDir, 0755); err != nil {
		tlog.ERROR.Printf("cannot create dir: %v, %v", imageDir, err)
		return
	}
	imgFile, err := os.Create(tempImgFileName)
	if err != nil {
		tlog.ERROR.Printf("cannot create image file: %v, %v", tempImgFileName, err)
		return
	}
	defer imgFile.Close()

	_, err = imgFile.Write(result.data)
	if err != nil {
		tlog.ERROR.Printf("failed to save data to file: %v, %v", tempImgFileName, err)
		return
	}
	imgFile.Sync()
	imgFile.Close()

	os.Rename(tempImgFileName, imageFileName)
	tlog.INFO.Printf("image %v downloaded, size: %d", imageFileName, len(result.data))
	downloaded = imageFileName
	return
}

func getAllImageUrls() (hImages []string, nhImages []string) {
	allImages := getImageUrls(false)
	nhImages = getImageUrls(true)
	hImages = difference(allImages, nhImages)
	return
}

func difference(a []string, b []string) (result []string) {
	m := make(map[string]bool)
	for _, i := range a {
		m[i] = true
	}
	for _, i := range b {
		delete(m, i)
	}
	for k := range m {
		result = append(result, k)
	}
	sort.Strings(result)
	return
}

// "http://acg.sugling.in/json_daily.php?device=iphone5&pro=yes&sexyfilter=no&version=k.5.0" User-Agent:"ACGArt/5.0.0.0 CFNetwork/711.1.16 Darwin/14.0.0" Accept:"*/*"
func getImageUrls(hfilter bool) []string {
	url := "http://acg.sugling.in/json_daily.php?device=iphone5&pro=yes&version=k.5.0&sexyfilter="
	if hfilter {
		url += "yes"
	} else {
		url += "no"
	}

	client := &http.Client{}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("User-Agent", `ACGArt/5.0.0.0 CFNetwork/711.1.16 Darwin/14.0.0`)
	req.Header.Add("Accept", `*/*`)
	resp, err := client.Do(req)
	if err != nil {
		panic(err.Error())
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)
	var res acgResult
	if err := json.Unmarshal(body, &res); err != nil {
		panic(err.Error())
	}
	var images []string
	for _, iSet := range res.Data {
		images = append(images, iSet.Imgs...)
	}
	return images
}

func getLocalImages() (hImages []string, nhImages []string) {
	var _ error
	hImages, _ = filepath.Glob("./images/H/*/*/*/*.jpg")
	for i := 0; i < len(hImages); i++ {
		hImages[i] = path.Base(hImages[i])
	}

	nhImages, _ = filepath.Glob("./images/NH/*/*/*/*.jpg")
	for i := 0; i < len(nhImages); i++ {
		nhImages[i] = path.Base(nhImages[i])
	}
	return
}

func goGetImages(h bool, concurrency int, urls []string) (downloaded []string) {
	throttle := make(chan int, concurrency)
	dataCh := make(chan downloadResult, concurrency)
	for _, u := range urls {
		go func(url string) {
			throttle <- 0
			dataCh <- retryDownloadImage(url, h)
			<-throttle
		}(u)
	}

	for i := 0; i < len(urls); i++ {
		si := saveImage(<-dataCh)
		if si != "" {
			downloaded = append(downloaded, si)
		}
	}
	return
}

func main() {
	verbose := flag.Bool("v", false, "verbose")
	download := flag.Bool("download", false, "download")
	flag.Parse()
	if *verbose {
		tlog.SetConsoleLogLevel(tlog.LevelDebug)
	} else {
		tlog.SetConsoleLogLevel(tlog.LevelInfo)
	}

	tlog.INFO.Println("Getting local images from disk...")
	hli, nhli := getLocalImages()
	tlog.INFO.Printf("Local H images count = %d", len(hli))
	tlog.INFO.Printf("Local non-H images count = %d", len(nhli))

	tlog.INFO.Println("Getting all image urls from ACG...")
	hi, nhi := getAllImageUrls()
	tlog.INFO.Printf("Total H images count = %d", len(hi))
	tlog.INFO.Printf("Total non-H images count = %d", len(nhi))

	htodown := difference(hi, hli)
	nhtodown := difference(nhi, nhli)
	tlog.INFO.Printf("Total H images to download = %d", len(htodown))
	tlog.INFO.Printf("Total non-H images to download = %d", len(nhtodown))

	if *download {
		hDownloaded := goGetImages(true, 10, htodown)
		nhDownloaded := goGetImages(false, 10, nhtodown)

		tlog.INFO.Printf("Done")
		tlog.INFO.Printf("Total H images downloaded = %d", len(hDownloaded))
		tlog.INFO.Printf("Total non-H images downloaded = %d", len(nhDownloaded))
	} else {
		tlog.WARN.Printf("-download not set, will not do actual work")
	}
}
