package main

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/shlex"
	"github.com/kjk/u"
)

// runs `go run ${path}` and returns captured output`
func getGoOutput(path string) (string, error) {
	dir, fileName := filepath.Split(path)
	cmd := exec.Command("go", "run", fileName)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func getRunCmdOutput(path string, runCmd string) (string, error) {
	parts, err := shlex.Split(runCmd)
	maybePanicIfErr(err)
	if err != nil {
		return "", err
	}
	exeName := parts[0]
	parts = parts[1:]
	var parts2 []string
	srcDir, srcFileName := filepath.Split(path)

	// remove empty lines and replace variables
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		switch part {
		case "$file":
			part = srcFileName
		}
		parts2 = append(parts2, part)
	}
	//fmt.Printf("getRunCmdOutput: running '%s' with args '%#v'\n", exeName, parts2)
	cmd := exec.Command(exeName, parts2...)
	cmd.Dir = srcDir
	out, err := cmd.CombinedOutput()
	//fmt.Printf("getRunCmdOutput: out:\n%s\n", string(out))
	return string(out), err
}

func stripCurrentPathFromOutput(s string) string {
	path, err := filepath.Abs(".")
	u.PanicIfErr(err)
	return strings.Replace(s, path, "", -1)
}

// it executes a code file and captures the output
// optional runCmd says
func getOutput(path string, runCmd string) (string, error) {
	if runCmd != "" {
		//fmt.Printf("Found :run cmd '%s' in '%s'\n", runCmd, path)
		s, err := getRunCmdOutput(path, runCmd)
		return stripCurrentPathFromOutput(s), err
	}

	// do default
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".go" {
		s, err := getGoOutput(path)
		return stripCurrentPathFromOutput(s), err
	}
	return "", fmt.Errorf("getOutput(%s): files with extension '%s' are not supported", path, ext)
}

var allowedLanguages = map[string]bool{
	"go":         true,
	"javascript": true,
	"cpp":        true,
}

func updateGlotID(cache *Cache, sf *SourceFile) error {
	panicIf(!sf.Directive.Glot)

	// we don't want it
	if sf.Directive.NoPlayground {
		return nil
	}

	// already have it
	if sf.GlotPlaygroundID != "" {
		return nil
	}

	// it's already in cache
	sha1 := sf.Sha1()
	id := cache.sha1ToGlotID[sha1]
	if id != "" {
		sf.GlotPlaygroundID = id
		sf.PlaygroundURI = "https://glot.io/snippets/" + sf.GlotPlaygroundID
		return nil
	}

	lang := strings.ToLower(sf.Lang)
	lang = glotConvertLanguage(lang)
	if _, ok := allowedLanguages[lang]; !ok {
		return fmt.Errorf("'%s' ('%s') is not a supported language", sf.Lang, lang)
	}

	fileName := sf.Directive.FileName
	snippetName := sf.SnippetName

	d := []byte(sf.CodeToRun)
	rsp, err := glotGetSnippedID(d, snippetName, fileName, lang)
	if err != nil {
		return err
	}
	id = rsp.ID
	sf.GlotPlaygroundID = id
	sf.PlaygroundURI = "https://glot.io/snippets/" + sf.GlotPlaygroundID

	cache.saveGlotID(sha1, id)
	return nil
}

func updateGlotOutput(cache *Cache, sf *SourceFile) error {
	// we already have it
	if sf.GlotOutput != "" {
		return nil
	}

	// it's already in cache
	o := cache.sha1ToGlotOutput[sf.Sha1()]
	if o != nil {
		sf.GlotOutput = o.Output
		return nil
	}

	f := &glotFile{
		Name:    sf.Directive.FileName,
		Content: sf.CodeToRun,
	}
	if sf.Directive.RunCmd != "" {
		lg("  run command: %s\n", sf.Directive.RunCmd)
	}
	req := &glotRunRequest{
		Command:  sf.Directive.RunCmd,
		language: sf.Lang,
		Files:    []*glotFile{f},
	}
	rsp, err := glotRun(req)
	if err != nil {
		lg("glotRun() failed. Page: %s\n", sf.NotionOriginURL)
		panicIfErr(err)
	}
	s := rsp.Stdout + rsp.Stderr
	if rsp.Error != "" {
		if !sf.Directive.AllowError {
			//fmt.Printf("getOutput('%s'), output is:\n%s\n", path, s)
			return errors.New(rsp.Stderr)
		}
	}
	sf.GlotOutput = s
	lg("Got glot output (%d bytes) for %s from %s\n", len(sf.GlotOutput), sf.SnippetName, sf.NotionOriginURL)

	o = &GlotOutput{
		Lang:      sf.Lang,
		FileName:  f.Name,
		CodeFull:  sf.CodeFull,
		CodeToRun: sf.CodeToRun,
		RunCmd:    sf.Directive.RunCmd,
		Output:    s,
	}
	if !flgNoUpdateOutput {
		cache.saveGlotOutput(o)
	}
	return nil
}

// for a given file, get output of executing this command
// We cache this as it is the most expensive part of rebuilding books
// If allowError is true, we silence an error from executed command
// This is useful when e.g. executing "go run" on a program that is
// intentionally not valid.
func getOutputCached(cache *Cache, sf *SourceFile) error {
	// TODO: make the check for when not to execute the file
	// even better
	if sf.Directive.NoOutput {
		if sf.Directive.Glot {
			// if running on glot, we want to execute even if
			// we don't show the output (to check syntax errors)
		} else {
			return nil
		}
	}

	if sf.Directive.Glot {
		err := updateGlotOutput(cache, sf)
		must(err)
		err = updateGlotID(cache, sf)
		must(err)
	}

	/*
		path := sf.Path
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".json", ".csv", ".yml", ".xml":
			return nil
		}

		// fmt.Printf("loadFileCached('%s') failed with '%s'\n", outputPath, err)
		s, err := getOutput(path, sf.Directive.RunCmd)
		if err != nil {
			if !sf.Directive.AllowError {
				lg("getOutput('%s'), got error:\n%s\n", path, s)
				return err
			}
			err = nil
		}
		sf.Output = s
		fmt.Printf("Got output (%d bytes) for '%s' by running locally\n", len(sf.Output), path)
	*/
	return nil
}
