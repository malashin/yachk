package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html/charset"

	ansi "github.com/k0kubun/go-ansi"
)

// apiURL and clientID for KinoPoisk api
// must be set here before compilation
var apiURL string
var clientID string

var regexpMap = map[string]*regexp.Regexp{
	"idQ":  regexp.MustCompile(`.*?(s\d{2}e\d{2,4})?(?:\_)?coid(\d+).*_q(\d+).*`),
	"idQ2": regexp.MustCompile(`.*?(s\d{2}e\d{2,4})?(?:\_)?coid-(\d+).*-q(\d+).*`),
	"0v":   regexp.MustCompile(`Stream #(\d+):(\d+)\(([A-Za-z]+)\): Video: ([0-9A-Za-z]+) .*, ([0-9A-Za-z]+), (\d+)x(\d+) \[SAR (\d+):(\d+) DAR (\d+):(\d+)\],.*, ((?:\d+)|(?:\d+\.\d+)) fps,.* (\(default\))?`),
	"1a":   regexp.MustCompile(`Stream #(\d+):(\d+)\(([A-Za-z]+)\): Audio: ([0-9A-Za-z]+) .*, (\d+) Hz, ([0-9A-Za-z\.]+).*,.* (\(default\))*?.*\n(?:.*Metadata:.*\n.*handler_name.*: (.*)?(?:\r\n))*`),
}

type video struct {
	stream     [2]int
	lang       string
	format     string
	pixFmt     string
	resolution [2]int
	sar        [2]int
	dar        [2]int
	fps        float64
	isDefault  bool
}

type audio struct {
	stream     [2]int
	lang       string
	format     string
	sampleRate string
	channels   string
	isDefault  bool
	name       string
}

type movie struct {
	Years         []int  `json:"years"`
	Title         string `json:"title"`
	OriginalTitle string `json:"originalTitle"`
	Type          string `json:"type"`
}

func consolePrint(str ...interface{}) {
	ansi.Print("\x1b[?25l") // Hide the cursor.
	ansi.Print(str...)
	ansi.Print("\x1b[?25h") // Show the cursor.
}

func help() {
	consolePrint("\x1b[31;1mNo arguments were provided.\x1b[0m\n")
	consolePrint("USAGE: yachk inputFile\n")
}

// parseVideo returns video parameters from FFmpeg string.
func parseVideo(in string) (video, error) {
	stream1, err := strconv.Atoi(regexpMap["0v"].ReplaceAllString(in, "${1}"))
	stream2, err := strconv.Atoi(regexpMap["0v"].ReplaceAllString(in, "${2}"))
	lang := regexpMap["0v"].ReplaceAllString(in, "${3}")
	format := regexpMap["0v"].ReplaceAllString(in, "${4}")
	pixFmt := regexpMap["0v"].ReplaceAllString(in, "${5}")
	resolution1, err := strconv.Atoi(regexpMap["0v"].ReplaceAllString(in, "${6}"))
	resolution2, err := strconv.Atoi(regexpMap["0v"].ReplaceAllString(in, "${7}"))
	sar1, err := strconv.Atoi(regexpMap["0v"].ReplaceAllString(in, "${8}"))
	sar2, err := strconv.Atoi(regexpMap["0v"].ReplaceAllString(in, "${9}"))
	dar1, err := strconv.Atoi(regexpMap["0v"].ReplaceAllString(in, "${10}"))
	dar2, err := strconv.Atoi(regexpMap["0v"].ReplaceAllString(in, "${11}"))
	fps, err := strconv.ParseFloat(regexpMap["0v"].ReplaceAllString(in, "${12}"), 64)
	isDefault := false
	if regexpMap["0v"].ReplaceAllString(in, "${13}") != "" {
		isDefault = true
	}
	return video{[2]int{stream1, stream2}, lang, format, pixFmt, [2]int{resolution1, resolution2}, [2]int{sar1, sar2}, [2]int{dar1, dar2}, fps, isDefault}, err
}

// parseAudio returns audio parameters from FFmpeg string.
func parseAudio(in string) (audio, error) {
	stream1, err := strconv.Atoi(regexpMap["1a"].ReplaceAllString(in, "${1}"))
	stream2, err := strconv.Atoi(regexpMap["1a"].ReplaceAllString(in, "${2}"))
	lang := regexpMap["1a"].ReplaceAllString(in, "${3}")
	format := regexpMap["1a"].ReplaceAllString(in, "${4}")
	sampleRate := regexpMap["1a"].ReplaceAllString(in, "${5}")
	channels := regexpMap["1a"].ReplaceAllString(in, "${6}")
	isDefault := false
	if regexpMap["1a"].ReplaceAllString(in, "${7}") != "" {
		isDefault = true
	}
	name := regexpMap["1a"].ReplaceAllString(in, "${8}")
	return audio{[2]int{stream1, stream2}, lang, format, sampleRate, channels, isDefault, name}, err
}

// getMetaFromKP takes KinoPoisk ID and returns movies name, year and type in strings.
func getMetaFromKP(id string) (string, string, string, error) {
	if clientID == "" {
		return "", "", "", errors.New("clientID for KinoPoisk api is not provided")
	}

	req, err := http.NewRequest("GET", apiURL+id, nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Clientid", clientID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()

	utf8, err := charset.NewReader(resp.Body, resp.Header.Get("Content-Type"))
	if err != nil {
		return "", "", "", fmt.Errorf("Encoding error: %v", err)
	}

	body, err := ioutil.ReadAll(utf8)
	if err != nil {
		return "", "", "", fmt.Errorf("IO error: %v", err)
	}

	m := movie{}
	err = json.Unmarshal(body, &m)
	if err != nil {
		return "", "", "", fmt.Errorf("json.Unmarshal error: %v", err)
	}

	movieName := ""
	if m.Title == "" {
		movieName, err = translit(m.OriginalTitle) // Use original title is Russian title is missing.
	} else {
		movieName, err = translit(m.Title)
	}
	if err != nil {
		return "", "", "", fmt.Errorf("TRANSLIT error: %v", err)
	}
	movieYear := strconv.Itoa(m.Years[0])

	return movieName, movieYear, m.Type, nil
}

func main() {
	kp := true
	args := os.Args[1:]

	// Show help info and exit if no arguments were passed.
	if len(args) < 1 {
		help()
		os.Exit(0)
	}

	// Iterate over passed arguments.
	for _, fileName := range args {
		// Remove path from filename.
		baseName := path.Base(filepath.ToSlash(fileName))

		// Print out input fileName.
		consolePrint("INPUT:  " + baseName + "\n")

		// Get KinoPoisk ID and Q from fileName.
		var se, id, q string
		if regexpMap["idQ"].MatchString(baseName) {
			se = regexpMap["idQ"].ReplaceAllString(baseName, "${1}")
			id = regexpMap["idQ"].ReplaceAllString(baseName, "${2}")
			q = regexpMap["idQ"].ReplaceAllString(baseName, "${3}")
		} else if regexpMap["idQ2"].MatchString(baseName) {
			se = regexpMap["idQ2"].ReplaceAllString(baseName, "${1}")
			id = regexpMap["idQ2"].ReplaceAllString(baseName, "${2}")
			q = regexpMap["idQ2"].ReplaceAllString(baseName, "${3}")
		}
		if id == baseName || id == "" || q == baseName || q == "" {
			consolePrint("\x1b[31;1m", "FileName is wrong.", "\x1b[0m\n")
			consolePrint("MUST BE: .*coid(\\d+)_q(\\d+).*\n\n")
			continue
		}

		// Get translitirated movie name, year and type (MOVIE, SHOW) from KinoPoisk api.
		movieName, movieYear, movieType, err := getMetaFromKP(id)
		if movieName == "" || movieYear == "" || movieType == "" {
			kp = false
			if err != nil {
				consolePrint("\x1b[31;1m", err, ".\x1b[0m\n")
			}
			consolePrint("\x1b[33;1m", "getMetaFromKP: Could not get data from KinoPoisk", "\x1b[0m\n")
		}

		// Get passed file info with ffmpeg.
		cmd := exec.Command("ffmpeg", "-hide_banner", "-i", fileName)
		stdoutStderr, err := cmd.CombinedOutput()
		if err != nil && fmt.Sprint(err) != "exit status 1" {
			consolePrint("\x1b[31;1m", err, "\x1b[0m\n")
		}

		// Get video track data.
		videoStrings := regexpMap["0v"].FindAllString(string(stdoutStderr), -1)
		if len(videoStrings) > 1 {
			consolePrint("\x1b[31;1mMore then one video stream.\x1b[0m\n")
			for _, v := range videoStrings {
				consolePrint(v, "\n")
			}
			consolePrint("\n")
			continue
		}
		if len(videoStrings) == 0 {
			consolePrint("\x1b[31;1mNo video stream found.\x1b[0m\n")
			continue
		}
		video, err := parseVideo(videoStrings[0])
		if err != nil {
			consolePrint("\x1b[31;1mCould not parse video stream.\x1b[0m\n")
			continue
		}

		// Get audio track data.
		audioStrings := regexpMap["1a"].FindAllString(string(stdoutStderr), -1)
		if len(audioStrings) > 1 {
			consolePrint("\x1b[31;1mMore then one audio stream.\x1b[0m\n")
			for _, v := range audioStrings {
				consolePrint(v, "\n\n")
			}
			continue
		}
		if len(audioStrings) == 0 {
			consolePrint("\x1b[31;1mNo audio stream found.\x1b[0m\n")
			continue
		}
		audio, err := parseAudio(audioStrings[0])
		if err != nil {
			consolePrint("\x1b[31;1mCould not parse audio stream.\x1b[0m\n")
			continue
		}

		// Checks for standard compliance.
		// Compare resolution with "q" (quality) stated in inport name.
		iq, _ := strconv.Atoi(q)
		if video.resolution[0] < 1900 && video.resolution[1] < 1040 && iq == 0 {
			consolePrint("\x1b[33;1m", "\"Q\" value might be too high, the number should be greater then zero.", "\x1b[0m\n")
		}
		if (video.resolution[0] >= 1900 || video.resolution[1] >= 1040) && iq > 0 {
			consolePrint("\x1b[33;1m", "\"Q\" value might be too low, the number should be equal to zero.", "\x1b[0m\n")
		}

		// Check if resolution is 720 and print out a message about possible error.
		if video.resolution[0] == 720 {
			consolePrint("\x1b[33;1m", "Horizontal resolution is 720. Input SAR might be wrong.", "\x1b[0m\n")
		}

		// Check if SAR is not 1:1 and return error message.
		if video.sar[0] != video.sar[1] {
			consolePrint("\x1b[31;1m", "SAR is not 1:1.", "\x1b[0m\n")
			continue
		}

		// Check if audio track is not stereo and return error message.
		if audio.channels != "stereo" {
			consolePrint("\x1b[31;1m", "Audio track has "+audio.channels+" channels.", "\x1b[0m\n")
			continue
		}

		// Convert audio channels to single digits.
		ac := "2"

		// Convert fps to string.
		fps := strconv.FormatFloat(video.fps, 'f', -1, 64)
		fps = strings.Replace(fps, ".", "", -1)

		// Add s00e00 to outputName if movie type is "SHOW".
		if movieType != "SHOW" {
			se = ""
		} else {
			if se == "" {
				consolePrint("\x1b[31;1m", "Season and episode numbers must follow \"s(\\d{2})e(\\d{2,4})\" rule.", "\x1b[0m\n")
				continue
			}
			se = "_" + se
		}

		// Construct correct fileName and compare it with input fileName.
		if kp {
			// If movieTitle and movieYear from KinoPoisk are available, add them to fileName.
			outputName := movieName + se + "_coid" + id + "_" + movieYear + "__q" + q + "_r" + strconv.Itoa(video.resolution[0]) + "x" + strconv.Itoa(video.resolution[1]) + "p" + fps + "_a" + string(audio.lang[0]) + ac + ".mp4"
			if baseName == outputName {
				consolePrint("\x1b[32;1m" + "FileName is correct." + "\x1b[0m\n")
			} else {
				consolePrint("OUTPUT: " + outputName + "\n")
				consolePrint("\x1b[31;1m" + "FileName is wrong." + "\x1b[0m\n")
			}
		} else {
			// If KinoPoisk is not available only check video and audio parameters.
			outputName := ".*" + se + "_coid" + id + "_" + ".*" + "__q" + q + "_r" + strconv.Itoa(video.resolution[0]) + "x" + strconv.Itoa(video.resolution[1]) + "p" + fps + "_a" + string(audio.lang[0]) + ac + ".mp4"
			re := regexp.MustCompile(outputName)
			if re.MatchString(baseName) {
				consolePrint("\x1b[32;1m" + "Video and audio parameters in input fileName are correct." + "\x1b[0m\n")
			} else {
				consolePrint("OUTPUT: " + outputName + "\n")
				consolePrint("\x1b[31;1m" + "Video or audio parameters in input fileName are wrong." + "\x1b[0m\n")
			}
		}
		consolePrint("\n")
	}
	consolePrint("Press 'Enter' to continue...")
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}
