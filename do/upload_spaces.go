package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kjk/u"
)

func newMinioClient() *u.MinioClient {
	res := &u.MinioClient{
		StorageKey:    os.Getenv("SPACES_KEY"),
		StorageSecret: os.Getenv("SPACES_SECRET"),
		Bucket:        "kjkpubsf",
		Endpoint:      "sfo2.digitaloceanspaces.com",
	}
	res.EnsureConfigured()
	return res
}

func hasSpacesCreds() bool {
	if os.Getenv("SPACES_KEY") == "" {
		logf("Not uploading to do spaces because SPACES_KEY env variable not set\n")
		return false
	}
	if os.Getenv("SPACES_SECRET") == "" {
		logf("Not uploading to do spaces because SPACES_SECRET env variable not set\n")
		return false
	}
	return true
}

func minioUploadFiles(c *u.MinioClient, prefix string, dir string, files []string) error {
	n := len(files) / 2
	for i := 0; i < n; i++ {
		pathLocal := filepath.Join(dir, files[2*i])
		pathRemote := prefix + files[2*i+1]
		err := c.UploadFilePublic(pathRemote, pathLocal)
		if err != nil {
			return fmt.Errorf("failed to upload '%s' as '%s', err: %s", pathLocal, pathRemote, err)
		}
		logf("Uploaded to spaces: '%s' as '%s'\n", pathLocal, pathRemote)
	}
	return nil
}

// upload as:
// https://kjkpubsf.sfo2.digitaloceanspaces.com/software/sumatrapdf/prerel/SumatraPDF-prerelease-1027-install.exe etc.
func spacesUploadPreReleaseMust(ver string, dir string) {
	if shouldSkipUpload() {
		return
	}
	if !hasSpacesCreds() {
		return
	}

	c := newMinioClient()
	timeStart := time.Now()
	preRelDir := "software/sumatrapdf/" + dir + "/"
	prefix := fmt.Sprintf("SumatraPDF-prerelease-%s", ver)
	manifestRemotePath := preRelDir + prefix + "-manifest.txt"
	files := []string{
		"SumatraPDF.exe", fmt.Sprintf("%s.exe", prefix),
		"SumatraPDF-dll.exe", fmt.Sprintf("%s-install.exe", prefix),
		"SumatraPDF.pdb.zip", fmt.Sprintf("%s.pdb.zip", prefix),
		"SumatraPDF.pdb.lzsa", fmt.Sprintf("%s.pdb.lzsa", prefix),
	}
	err := minioUploadFiles(c, preRelDir, filepath.Join("out", "rel32"), files)
	fatalIfErr(err)

	prefix = fmt.Sprintf("SumatraPDF-prerelease-%s-64", ver)
	files = []string{
		"SumatraPDF.exe", fmt.Sprintf("%s.exe", prefix),
		"SumatraPDF-dll.exe", fmt.Sprintf("%s-install.exe", prefix),
		"SumatraPDF.pdb.zip", fmt.Sprintf("%s.pdb.zip", prefix),
		"SumatraPDF.pdb.lzsa", fmt.Sprintf("%s.pdb.lzsa", prefix),
	}

	err = minioUploadFiles(c, preRelDir, filepath.Join("out", "rel64"), files)
	fatalIfErr(err)

	manifestLocalPath := filepath.Join(artifactsDir, "manifest.txt")
	err = c.UploadFilePublic(manifestRemotePath, manifestLocalPath)
	fatalIfErr(err)
	logf("Uploaded to spaces: '%s' as '%s'\n", manifestLocalPath, manifestRemotePath)

	minioUploadDailyInfo(c, ver, dir)

	logf("Uploaded the build to spaces in %s\n", time.Since(timeStart))
}

func minioUploadDailyInfo(c *u.MinioClient, ver string, dir string) {
	s := createSumatraLatestJs(dir)
	remotePath := "software/sumatrapdf/sumadaily.js"
	err := c.UploadDataPublic(remotePath, []byte(s))
	fatalIfErr(err)
	logf("Uploaded to spaces: '%s'\n", remotePath)

	//sumatrapdf/sumpdf-prerelease-latest.txt
	remotePath = "software/sumatrapdf/sumpdf-daily-latest.txt"
	err = c.UploadDataPublic(remotePath, []byte(ver))
	fatalIfErr(err)
	logf("Uploaded to spaces: '%s'\n", remotePath)

	//sumatrapdf/sumpdf-prerelease-update.txt
	//don't set a Stable version for pre-release builds
	s = fmt.Sprintf("[SumatraPDF]\nLatest %s\n", ver)
	remotePath = "software/sumatrapdf/sumpdf-daily-update.txt"
	err = c.UploadDataPublic(remotePath, []byte(s))
	fatalIfErr(err)
	logf("Uploaded to spaces: '%s'\n", remotePath)
}

// software/sumatrapdf/prerel/SumatraPDF-prerelease-11290-64-install.exe
func extractVersionFromName(s string) int {
	parts := strings.Split(s, "/")
	name := parts[len(parts)-1]
	name = strings.TrimPrefix(name, "SumatraPDF-prerelease-")

	// TODO: temporary, for old builds in s3
	name = strings.TrimPrefix(name, "SumatraPDF-prerelase-")
	name = strings.TrimPrefix(name, "manifest-")

	parts = strings.Split(name, "-")
	parts = strings.Split(parts[0], ".")
	verStr := parts[0]
	ver, err := strconv.Atoi(verStr)
	must(err)
	return ver
}

type filesByVer struct {
	ver   int
	files []string
}

func groupFilesByVersion(files []string) []*filesByVer {
	m := map[int]*filesByVer{}
	for _, f := range files {
		ver := extractVersionFromName(f)
		i := m[ver]
		if i == nil {
			i = &filesByVer{
				ver: ver,
			}
			m[ver] = i
		}
		i.files = append(i.files, f)
	}
	res := []*filesByVer{}
	for _, v := range m {
		res = append(res, v)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].ver > res[j].ver
	})
	return res
}

const nBuildsLeft = 8

func minioDeleteOldBuildsPrefix(prefix string) {
	c := newMinioClient()
	files, err := c.ListRemoteFiles(prefix)
	must(err)
	fmt.Printf("%d minio files under '%s'\n", len(files), prefix)
	var keys []string
	for _, f := range files {
		keys = append(keys, f.Key)
		//fmt.Printf("key: %s\n", f.Key)
	}
	byVer := groupFilesByVersion(keys)
	for i, v := range byVer {
		deleting := (i >= nBuildsLeft)
		if deleting {
			fmt.Printf("%d, deleting\n", v.ver)
			for _, fn := range v.files {
				fmt.Printf("  %s deleting\n", fn)
				err := c.Delete(fn)
				must(err)
			}
		} else {
			fmt.Printf("%d, not deleting\n", v.ver)
			// for _, fn := range v.files {
			// 	fmt.Printf("  %s not deleting\n", fn)
			// }
		}
	}
}

func minioDeleteOldBuilds() {
	minioDeleteOldBuildsPrefix("software/sumatrapdf/prerel/")
	minioDeleteOldBuildsPrefix("software/sumatrapdf/daily/")
}
