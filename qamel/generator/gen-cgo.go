package generator

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	fp "path/filepath"
	"regexp"
	"strings"

	"github.com/RadhiFadlillah/qamel/qamel/config"
)

var (
	rxMakefile    = regexp.MustCompile(`^(\S+)\s*=\s*(.+)$`)
	rxCompilerVar = regexp.MustCompile(`\$\((\S+)\)`)
)

// CreateCgoFile creates cgo file in specified package,
// using cgo flags that generated by CreateCgoFlags().
func CreateCgoFile(profile config.Profile, dstDir string, pkgName string) error {
	// Make sure target directory is exists
	err := os.MkdirAll(dstDir, os.ModePerm)
	if err != nil {
		return err
	}

	// Get the package name
	if pkgName == "" {
		pkgName, err = getPackageNameFromDir(dstDir)
		if err != nil {
			return err
		}
	}

	// Create cgo flags
	cgoFlags, err := createCgoFlags(profile, dstDir)
	if err != nil {
		return fmt.Errorf("failed to create cgo flags: %v", err)
	}

	// Create destination file
	fileName := fp.Join(dstDir, "qamel-cgo-"+pkgName+".go")
	dstFile, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	fileContent := fmt.Sprintln("package " + pkgName)
	fileContent += fmt.Sprintln()
	fileContent += fmt.Sprintln("/*")
	fileContent += fmt.Sprintln(cgoFlags)
	fileContent += fmt.Sprintln("*/")
	fileContent += fmt.Sprintln(`import "C"`)

	_, err = dstFile.WriteString(fileContent)
	if err != nil {
		return err
	}

	return dstFile.Sync()
}

// createCgoFlags creates cgo flags by using qmake
func createCgoFlags(profile config.Profile, dstDir string) (string, error) {
	// Create project file
	proContent := "QT += qml quick widgets svg\n"
	proContent += "CONFIG += release\n"
	if profile.OS == "windows" {
		proContent += "CONFIG += windows\n"
	}

	proFilePath := fp.Join(dstDir, "qamel.pro")
	proFile, err := os.Create(proFilePath)
	if err != nil {
		return "", err
	}
	defer proFile.Close()

	_, err = proFile.WriteString(proContent)
	if err != nil {
		return "", err
	}
	proFile.Sync()

	// Create makefile from project file using qmake
	makeFilePath := fp.Join(dstDir, "qamel.makefile")

	qmakeSpec := ""
	switch profile.OS {
	case "darwin":
		qmakeSpec = "macx-clang"
	case "linux":
		qmakeSpec = "linux-g++"
	case "windows":
		qmakeSpec = "win32-g++"
	}

	gccDir := fp.Dir(profile.Gcc)
	gxxDir := fp.Dir(profile.Gxx)
	envPath := os.Getenv("PATH")
	pathSeparator := ":"

	if profile.OS == "windows" {
		pathSeparator = ";"
	}

	if fileExists(profile.Gcc) {
		envPath = fmt.Sprintf(`%s%s%s`, gccDir, pathSeparator, envPath)
	}

	if fileExists(profile.Gxx) && gxxDir != gccDir {
		envPath = fmt.Sprintf(`%s%s%s`, gxxDir, pathSeparator, envPath)
	}

	cmdQmake := exec.Command(profile.Qmake, "-o", makeFilePath, "-spec", qmakeSpec, proFilePath)
	cmdQmake.Dir = dstDir
	cmdQmake.Env = append(os.Environ(), "PATH="+envPath)
	if btOutput, err := cmdQmake.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v\n%s", err, btOutput)
	}

	// Parse makefile
	qmakeResultPath := makeFilePath
	if profile.OS == "windows" {
		qmakeResultPath += ".Release"
	}

	mapCompiler := map[string]string{}
	makeFile, err := os.Open(qmakeResultPath)
	if err != nil {
		return "", err
	}
	defer makeFile.Close()

	scanner := bufio.NewScanner(makeFile)
	for scanner.Scan() {
		text := scanner.Text()
		parts := rxMakefile.FindStringSubmatch(text)
		if len(parts) != 3 {
			continue
		}

		mapCompiler[parts[1]] = parts[2]
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	// Convert variable in compiler flags
	for flagKey, flagValue := range mapCompiler {
		variables := rxCompilerVar.FindAllString(flagValue, -1)
		for _, variable := range variables {
			variableKey := rxCompilerVar.ReplaceAllString(variable, "$1")
			variableValue := mapCompiler[variableKey]
			flagValue = strings.Replace(flagValue, variable, variableValue, -1)
		}

		// Go does not support big-obj files yet (see https://github.com/golang/go/issues/24341).
		// However, qmake in mingw64 uses them by default. To bypass it, we need to remove `-Wa,-mbig-obj` flags.
		flagValue = strings.Replace(flagValue, " -Wa,-mbig-obj ", " ", -1)
		mapCompiler[flagKey] = strings.TrimSpace(flagValue)
	}

	// Fetch the needed flags for cgo
	cgoFlags := fmt.Sprintf("#cgo CFLAGS: %s\n", mapCompiler["CFLAGS"])
	cgoFlags += fmt.Sprintf("#cgo CXXFLAGS: %s\n", mapCompiler["CXXFLAGS"])
	cgoFlags += fmt.Sprintf("#cgo CXXFLAGS: %s\n", mapCompiler["INCPATH"])
	cgoFlags += fmt.Sprintf("#cgo LDFLAGS: %s\n", mapCompiler["LFLAGS"])
	cgoFlags += fmt.Sprintf("#cgo LDFLAGS: %s\n", mapCompiler["LIBS"])
	cgoFlags += fmt.Sprintln("#cgo CFLAGS: -Wno-unused-parameter -Wno-unused-variable -Wno-return-type")
	cgoFlags += fmt.Sprint("#cgo CXXFLAGS: -Wno-unused-parameter -Wno-unused-variable -Wno-return-type")

	// Remove generated file and folder
	os.Remove(proFilePath)
	os.Remove(makeFilePath)
	os.Remove(makeFilePath + ".Debug")
	os.Remove(makeFilePath + ".Release")
	os.Remove(fp.Join(dstDir, ".qmake.stash"))

	debugDir := fp.Join(dstDir, "debug")
	if dirExists(debugDir) && dirEmpty(debugDir) {
		os.RemoveAll(debugDir)
	}

	releaseDir := fp.Join(dstDir, "release")
	if dirExists(releaseDir) && dirEmpty(releaseDir) {
		os.RemoveAll(releaseDir)
	}

	return cgoFlags, nil
}
