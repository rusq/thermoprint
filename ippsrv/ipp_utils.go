package ippsrv

// contains supplemental functions for value conversion and other convenience.

import (
	"fmt"

	"github.com/OpenPrinting/goipp"
)

const (
	ippNone           goipp.String = "none"
	ippUTF8           goipp.String = "utf-8"
	ippENUS           goipp.String = "en-us"
	ippApplicationPDF goipp.String = "application/pdf"
	ippImageURF       goipp.String = "image/urf"
)

// adder is a helper function to add attributes to an operation.
func adder(op goipp.Attributes) func(s string, tag goipp.Tag, values ...goipp.Value) {
	return func(name string, tag goipp.Tag, values ...goipp.Value) {
		if len(values) == 0 {
			values = []goipp.Value{goipp.String("")}
		}
		attr := goipp.MakeAttribute(name, tag, values[0])
		for _, v := range values[1:] {
			attr.Values.Add(tag, v)
		}
		op.Add(attr)
	}
}

func stringsToValues[S ~[]E, E ~string](strs S) []goipp.Value {
	// Convert []string to []goipp.Value
	values := make([]goipp.Value, len(strs))
	for i, str := range strs {
		values[i] = goipp.String(str)
	}
	return values
}

// https://datatracker.ietf.org/doc/html/rfc8011#section-4.1.6
// https://datatracker.ietf.org/doc/html/rfc8011#appendix-B
type statusCode string

const (
	scInformational statusCode = "informational"
	scSuccessful    statusCode = "successful"
	scRedirection   statusCode = "redirection"
	scClientError   statusCode = "client-error"
	scServerError   statusCode = "server-error"
)

const (
	codeOK     = 0
	requestNum = 1
)

func baseResponse(s statusCode) *goipp.Message {
	m := goipp.NewRequest(goipp.DefaultVersion, codeOK, requestNum)
	a := adder(m.Operation)
	a("attributes-charset", goipp.TagCharset, ippUTF8)
	a("attributes-natural-language", goipp.TagLanguage, ippENUS)
	a("status-code", goipp.TagKeyword, goipp.String(s))
	return m
}

func findAttr(attrs goipp.Attributes, name string) (goipp.Values, bool) {
	for _, attr := range attrs {
		if attr.Name == name && len(attr.Values) > 0 {
			return attr.Values, true
		}
	}
	return nil, false
}

func extractValue[T any](attrs goipp.Attributes, name string) (T, error) {
	var zero T
	vv, ok := findAttr(attrs, name)
	if !ok || len(vv) == 0 {
		return zero, fmt.Errorf("attribute %q not found", name)
	}
	if len(vv) > 1 {
		return zero, fmt.Errorf("attribute %q has multiple values: %d", name, len(vv))
	}
	v := vv[0].V
	if val, ok := v.(T); ok {
		return val, nil
	}
	return zero, fmt.Errorf("attribute %q is not of type %T: %T", name, zero, v)
}
