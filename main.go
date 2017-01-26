package main

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"os"

	"github.com/dewey/whatapi"
	"github.com/fatih/color"
)

// DirectoryListing contains all files in a directory
type DirectoryListing []string

func (d DirectoryListing) Len() int {
	return len(d)
}
func (d DirectoryListing) Swap(i, j int) {
	d[i], d[j] = d[j], d[i]
}
func (d DirectoryListing) Less(i, j int) bool {
	return len(d[i]) < len(d[j])
}

var reFileList = regexp.MustCompile(`(.*?)\{{3}\d*\}{3}(?:\|{3})?`)

func main() {
	s := color.New(color.FgGreen).SprintFunc()
	f := color.New(color.FgRed).SprintFunc()

	cwd, err := os.Getwd()
	if err != nil {
		log.Print(err)
	}
	pth, err := whatapi.NewWhatAPI("https://passtheheadphones.me/", "")
	if err != nil {
		log.Fatal(err)
	}

	if os.Getenv("WF_USERNAME") == "" && os.Getenv("WF_PASSWORD") == "" {
		log.Fatal(errors.New("username and password are not set via ENV variable"))
	}

	err = pth.Login(os.Getenv("WF_USERNAME"), os.Getenv("WF_PASSWORD"))
	if err != nil {
		log.Fatal(err)
	}

	acc, err := pth.GetAccount()
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Logged in as: %s", acc.Username)

	// Directory where files are stored
	fp := path.Join(cwd, "files")

	files, err := ioutil.ReadDir(fp)
	if err != nil {
		log.Fatal(err)
	}

	for _, file := range files {
		// Skip files with a dot (.DS_STORE and other trash)
		if strings.HasPrefix(file.Name(), ".") {
			continue
		}

		subDir, err := ioutil.ReadDir(path.Join(fp, file.Name()))
		if err != nil {
			log.Fatal(err)
		}

		var fileList DirectoryListing
		for _, subFile := range subDir {
			// Filter out invisible files or shit
			if !strings.HasPrefix(subFile.Name(), ".") {
				fileList = append(fileList, subFile.Name())
			}
		}
		log.Printf("Checking local directory: %s, File count: %d", file.Name(), len(fileList))

		sort.Sort(sort.Reverse(fileList))
		if len(fileList) > 3 {
			var checkTotalCounter int
			var dlTorrentID int

			for i := 0; i < 3; i++ {
				log.Printf("> Test local file %d against site: %s, length: %d", i, fileList[i], len(fileList[i]))
				time.Sleep(time.Second * 5)
				sr, err := pth.SearchTorrentsByFilename(fileList[i], url.Values{})
				if err != nil {
					log.Print(err)
				}
				if len(sr.Results) > 0 {
					log.Print("  Torrent group results:")
				} else {
					log.Print("no search results, check next local directory")
					break
				}
				for _, r := range sr.Results {
					log.Printf("   - Artist: %s, Album: %s, Year: %d", r.Artist, r.GroupName, r.GroupYear)
					for _, t := range r.Torrents {
						// Does the file name have a match on the site?
						var checkCounter int
						var torrentID int

						// The current album
						formatString := fmt.Sprintf("      Format: %s, Encoding: %s, File count: %d", t.Format, t.Encoding, t.FileCount)
						if t.FileCount == len(fileList) {
							// Current torrent we are looking at
							log.Printf(formatString)

							// Check if number of files is the same
							log.Printf("%s", s("      ✔︎ File count matched"))
							checkCounter++

							time.Sleep(time.Second * 2)
							torrentDetails, err := pth.GetTorrent(t.TorrentID, url.Values{})
							if err != nil {
								log.Print(err)
							}
							// Does directory match?
							if torrentDetails.Torrent.FilePath == file.Name() {
								log.Printf("%s", s("      ✔︎ Directory name matched"))
								checkCounter++
								torrentID = torrentDetails.Torrent.ID
							}
							// Check if all files are the same
							var fileListRemote DirectoryListing
							fileListResults := reFileList.FindAllStringSubmatch(torrentDetails.Torrent.FileList, -1)

							for _, fn := range fileListResults {
								if len(fn) > 1 {
									fileListRemote = append(fileListRemote, fn[1])
								}
							}
							if listingEqual(fileList, fileListRemote) {
								log.Printf("%s", s("      ✔︎ File names matched"))
								checkCounter++
								if torrentID != torrentDetails.Torrent.ID {
									log.Print("Different torrent IDs, something's wrong")
								} else {
									torrentID = torrentDetails.Torrent.ID
								}

							} else {
								log.Printf("%s", f("      ⚡︎ File names didn't match"))
							}
						} else {
							log.Printf(formatString)
							log.Printf("%s", f("      ⚡︎ File count different"))

						}
						if checkCounter == 3 {
							checkTotalCounter++
							dlTorrentID = torrentID
						}
					}

				}
				if checkTotalCounter == 3 {
					log.Printf("%s", s(">> ✔︎ All checks for multiple files successful, download torrent file"))

					url, err := pth.CreateDownloadURL(dlTorrentID)
					if err != nil {
						log.Print(err)
					}
					err = downloadTorrent(path.Join(cwd, "torrent"), url)
					if err != nil {
						log.Print(err)
					}
				}
			}
		} else {
			log.Print("Not enough files to test")
		}

	}
	if pth.Logout() != nil {
		log.Fatal(err)
	}
	log.Print("Logged out")
}

// listingEqual compares two directory listings
func listingEqual(f1 DirectoryListing, f2 DirectoryListing) bool {
	sort.Strings(f1)
	sort.Strings(f2)
	for i := range f1 {
		if f1[i] != f2[i] {
			return false
		}
	}
	return true
}

// downloadTorrent gets the file from the site and names it correctly
func downloadTorrent(filepath string, url string) error {
	out, err := os.Create(path.Join(filepath, "temp.torrent"))
	if err != nil {
		return err
	}
	defer out.Close()

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if err != nil {
		return err
	}
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	// Rename file
	os.Rename(path.Join(filepath, "temp.torrent"), path.Join(filepath, params["filename"]))

	return nil
}
