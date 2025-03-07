package analyzer

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"

	"github.com/Checkmarx/kics/internal/metrics"
	"github.com/Checkmarx/kics/pkg/model"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	yamlParser "gopkg.in/yaml.v3"
)

// openAPIRegex - Regex that finds OpenAPI defining property "openapi"
// openAPIRegexInfo - Regex that finds OpenAPI defining property "info"
// openAPIRegexPath - Regex that finds OpenAPI defining property "paths"
// cloudRegex - Regex that finds Cloud Formation defining property "Resources"
// k8sRegex - Regex that finds Kubernetes defining property "apiVersion"
// k8sRegexKind - Regex that finds Kubernetes defining property "kind"
// k8sRegexMetadata - Regex that finds Kubernetes defining property "metadata"
// k8sRegexSpec - Regex that finds Kubernetes defining property "spec"
var (
	openAPIRegex                      = regexp.MustCompile("(\\s*\"openapi\":)|(\\s*openapi:)|(\\s*\"swagger\":)|(\\s*swagger:)")
	openAPIRegexInfo                  = regexp.MustCompile("(\\s*\"info\":)|(\\s*info:)")
	openAPIRegexPath                  = regexp.MustCompile("(\\s*\"paths\":)|(\\s*paths:)")
	armRegexContentVersion            = regexp.MustCompile("\\s*\"contentVersion\":")
	armRegexResources                 = regexp.MustCompile("\\s*\"resources\":")
	cloudRegex                        = regexp.MustCompile("(\\s*\"Resources\":)|(\\s*Resources:)")
	k8sRegex                          = regexp.MustCompile("(\\s*\"apiVersion\":)|(\\s*apiVersion:)")
	k8sRegexKind                      = regexp.MustCompile("(\\s*\"kind\":)|(\\s*kind:)")
	k8sRegexMetadata                  = regexp.MustCompile("(\\s*\"metadata\":)|(\\s*metadata:)")
	ansibleVaultRegex                 = regexp.MustCompile(`^\s*\$ANSIBLE_VAULT.*`)
	tfPlanRegexPV                     = regexp.MustCompile("\\s*\"planned_values\":")
	tfPlanRegexRC                     = regexp.MustCompile("\\s*\"resource_changes\":")
	tfPlanRegexConf                   = regexp.MustCompile("\\s*\"configuration\":")
	tfPlanRegexTV                     = regexp.MustCompile("\\s*\"terraform_version\":")
	cdkTfRegexMetadata                = regexp.MustCompile("\\s*\"metadata\":")
	cdkTfRegexStackName               = regexp.MustCompile("\\s*\"stackName\":")
	cdkTfRegexTerraform               = regexp.MustCompile("\\s*\"terraform\":")
	blueprintArtifactsRegexKind       = regexp.MustCompile("(\\s*\"kind\":)|(\\s*kind:)")
	blueprintArtifactsRegexProperties = regexp.MustCompile("(\\s*\"properties\":)|(\\s*properties:)")
	blueprintRegexTargetScope         = regexp.MustCompile("(\\s*\"targetScope\":)|(\\s*targetScope:)")
	blueprintRegexProperties          = regexp.MustCompile("(\\s*\"properties\":)|(\\s*properties:)")
	buildahRegex                      = regexp.MustCompile(`\s*buildah\s*from\s*\w+`)
)

var (
	listKeywordsGoogleDeployment = []string{"resources"}
)

const (
	yml        = ".yml"
	yaml       = ".yaml"
	json       = ".json"
	arm        = "azureresourcemanager"
	kubernetes = "kubernetes"
)

// Analyze will go through the slice paths given and determine what type of queries should be loaded
// should be loaded based on the extension of the file and the content
func Analyze(paths []string) (model.AnalyzedPaths, error) {
	// start metrics for file analyzer
	metrics.Metric.Start("file_type_analyzer")
	returnAnalyzedPaths := model.AnalyzedPaths{
		Types: make([]string, 0),
		Exc:   make([]string, 0),
	}

	var files []string
	var wg sync.WaitGroup
	// results is the channel shared by the workers that contains the types found
	results := make(chan string)

	// get all the files inside the given paths
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			return returnAnalyzedPaths, errors.Wrap(err, "failed to analyze path")
		}
		if err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			if !info.IsDir() {
				files = append(files, path)
			}
			return nil
		}); err != nil {
			log.Error().Msgf("failed to analize path %s: %s", path, err)
		}
	}

	// unwanted is the channel shared by the workers that contains the unwanted files that the parser will ignore
	unwanted := make(chan string, len(files))

	for _, file := range files {
		wg.Add(1)
		// analyze the files concurrently
		go worker(file, results, unwanted, &wg)
	}

	go func() {
		// close channel results when the worker has finished writing into it
		defer func() {
			close(unwanted)
			close(results)
		}()
		wg.Wait()
	}()

	availableTypes := createSlice(results)
	unwantedPaths := createSlice(unwanted)
	returnAnalyzedPaths.Types = availableTypes
	returnAnalyzedPaths.Exc = unwantedPaths
	// stop metrics for file analyzer
	metrics.Metric.Stop()
	return returnAnalyzedPaths, nil
}

// worker determines the type of the file by ext (dockerfile and terraform)/content and
// writes the answer to the results channel
// if no types were found, the worker will write the path of the file in the unwanted channel
func worker(path string, results, unwanted chan<- string, wg *sync.WaitGroup) {
	defer wg.Done()
	ext := filepath.Ext(path)
	if ext == "" {
		ext = filepath.Base(path)
	}
	switch ext {
	// Dockerfile
	case ".dockerfile", "Dockerfile":
		results <- "dockerfile"
	// Terraform
	case ".tf", "tfvars":
		results <- "terraform"
	// GRPC
	case ".proto":
		results <- "grpc"
	case ".sh":
		checkContent(path, results, unwanted, ext)
	// Cloud Formation, Ansible, OpenAPI
	case yaml, yml, json:
		checkContent(path, results, unwanted, ext)
	}
}

// regexSlice is a struct to contain a slice of regex
type regexSlice struct {
	regex []*regexp.Regexp
}

// types is a map that contains the regex by type
var types = map[string]regexSlice{
	"openapi": {
		regex: []*regexp.Regexp{
			openAPIRegex,
			openAPIRegexInfo,
			openAPIRegexPath,
		},
	},
	"kubernetes": {
		regex: []*regexp.Regexp{
			k8sRegex,
			k8sRegexKind,
			k8sRegexMetadata,
		},
	},
	"cloudformation": {
		regex: []*regexp.Regexp{
			cloudRegex,
		},
	},
	"azureresourcemanager": {
		[]*regexp.Regexp{
			armRegexContentVersion,
			armRegexResources,
		},
	},
	"terraform": {
		[]*regexp.Regexp{
			tfPlanRegexConf,
			tfPlanRegexPV,
			tfPlanRegexRC,
			tfPlanRegexTV,
		},
	},
	"cdkTf": {
		[]*regexp.Regexp{
			cdkTfRegexMetadata,
			cdkTfRegexStackName,
			cdkTfRegexTerraform,
		},
	},
	"blueprintsartifacts": {
		[]*regexp.Regexp{
			blueprintArtifactsRegexKind,
			blueprintArtifactsRegexProperties,
		},
	},
	"blueprint": {
		[]*regexp.Regexp{
			blueprintRegexTargetScope,
			blueprintRegexProperties,
		},
	},
	"buildah": {
		[]*regexp.Regexp{
			buildahRegex,
		},
	},
}

// overrides k8s match when all regexs passes for azureresourcemanager key and extension is set to json
func needsOverride(check bool, returnType, key, ext string) bool {
	if check && returnType == kubernetes && key == "azureresourcemanager" && ext == json {
		return true
	}
	return false
}

// checkContent will determine the file type by content when worker was unable to
// determine by ext, if no type was determined checkContent adds it to unwanted channel
func checkContent(path string, results, unwanted chan<- string, ext string) {
	// get file content
	content, err := os.ReadFile(path)
	if err != nil {
		log.Error().Msgf("failed to analyze file: %s", err)
		return
	}

	returnType := ""

	// Sort map so that CloudFormation (type that as less requireds) goes last
	keys := make([]string, 0, len(types))
	for k := range types {
		keys = append(keys, k)
	}

	sort.Sort(sort.Reverse(sort.StringSlice(keys)))

	for _, key := range keys {
		check := true
		for _, typeRegex := range types[key].regex {
			if res := typeRegex.Match(content); !res {
				check = false
				break
			}
		}
		// If all regexs passed and there wasn't a type already assigned
		if check && returnType == "" {
			returnType = key
		} else if needsOverride(check, returnType, key, ext) {
			returnType = key
		}
	}
	returnType = checkReturnType(path, returnType, ext, content)
	if returnType != "" {
		results <- returnType
		return
	}
	// No type was determined (ignore on parser)
	unwanted <- path
}

func checkReturnType(path, returnType, ext string, content []byte) string {
	if returnType != "" {
		if returnType == "cdkTf" {
			return "terraform"
		}
		if returnType == "blueprint" || returnType == "blueprintsartifacts" {
			return arm
		}
	} else if ext == yaml || ext == yml {
		if checkHelm(path) {
			return kubernetes
		}
		platform := checkYamlPlatform(content)
		if platform != "" {
			return platform
		}
	}
	return returnType
}

func checkHelm(path string) bool {
	_, err := os.Stat(filepath.Join(filepath.Dir(path), "Chart.yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return false
	} else if err != nil {
		log.Error().Msgf("failed to check helm: %s", err)
	}

	return true
}

func checkYamlPlatform(content []byte) string {
	var yamlContent model.Document
	if err := yamlParser.Unmarshal(content, &yamlContent); err != nil {
		log.Warn().Msgf("failed to parse yaml file: %s", err)
	}
	// check if it is google deployment manager platform
	for _, keyword := range listKeywordsGoogleDeployment {
		if _, ok := yamlContent[keyword]; ok {
			return "googledeploymentmanager"
		}
	}
	// check if it is an ansible vault
	if res := ansibleVaultRegex.Match(content); !res {
		// Since Ansible has no defining property
		// and no other type matched for YAML file extension, assume the file type is Ansible
		return "ansible"
	}
	return ""
}

// createSlice creates a slice from the channel given removing any duplicates
func createSlice(chanel chan string) []string {
	slice := make([]string, 0)
	for i := range chanel {
		if !contains(slice, i) {
			slice = append(slice, i)
		}
	}
	return slice
}

// contains is a simple method to check if a slice
// contains an entry
func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
