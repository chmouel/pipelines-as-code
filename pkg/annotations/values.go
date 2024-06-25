package annotations

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
)

const (
	// regex allows array of string or a single string
	// eg. ["foo", "bar"], ["foo"] or "foo".
	reValidateTag = `^\[(.*)\]$|^[^[\]\s]*$`
)

func GrabValuesFromAnnotations(annotations map[string]string, annotationReg string) ([]string, error) {
	rtareg := regexp.MustCompile(fmt.Sprintf("%s/%s", pipelinesascode.GroupName, annotationReg))
	var ret []string
	for annotationK, annotationV := range annotations {
		if !rtareg.MatchString(annotationK) {
			continue
		}
		items, err := GetAnnotationValues(annotationV)
		if err != nil {
			return ret, err
		}
		ret = append(items, ret...)
	}
	return ret, nil
}

// TODO: move to another file since it's common to all annotations_* files.
func GetAnnotationValues(annotation string) ([]string, error) {
	re := regexp.MustCompile(reValidateTag)
	annotation = strings.TrimSpace(annotation)
	match := re.MatchString(annotation)
	if !match {
		return nil, fmt.Errorf("annotations in pipeline are in wrong format: %s", annotation)
	}

	// if it's not an array then it would be a single string
	if !strings.HasPrefix(annotation, "[") {
		return []string{annotation}, nil
	}

	// Split all tasks by comma and make sure to trim spaces in there
	split := strings.Split(re.FindStringSubmatch(annotation)[1], ",")
	for i := range split {
		split[i] = strings.TrimSpace(split[i])
	}

	if split[0] == "" {
		return nil, fmt.Errorf("annotation \"%s\" has empty values", annotation)
	}

	return split, nil
}
