/*
Copyright 2014 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resource

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/meta"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util/errors"
)

// Builder provides convenience functions for taking arguments and parameters
// from the command line and converting them to a list of resources to iterate
// over using the Visitor interface.
type Builder struct {
	mapper *Mapper

	errs []error

	paths  []Visitor
	stream bool
	dir    bool

	selector  labels.Selector
	selectAll bool

	resources []string

	namespace string
	names     []string

	resourceTuples []resourceTuple

	defaultNamespace bool
	requireNamespace bool

	flatten bool
	latest  bool

	singleResourceType bool
	continueOnError    bool
}

type resourceTuple struct {
	Resource string
	Name     string
}

// NewBuilder creates a builder that operates on generic objects.
func NewBuilder(mapper meta.RESTMapper, typer runtime.ObjectTyper, clientMapper ClientMapper) *Builder {
	return &Builder{
		mapper: &Mapper{typer, mapper, clientMapper},
	}
}

// Filename is parameters passed via a filename argument which may be URLs, the "-" argument indicating
// STDIN, or paths to files or directories. If ContinueOnError() is set prior to this method being called,
// objects on the path that are unrecognized will be ignored (but logged at V(2)).
func (b *Builder) FilenameParam(paths ...string) *Builder {
	for _, s := range paths {
		switch {
		case s == "-":
			b.Stdin()
		case strings.Index(s, "http://") == 0 || strings.Index(s, "https://") == 0:
			url, err := url.Parse(s)
			if err != nil {
				b.errs = append(b.errs, fmt.Errorf("the URL passed to filename %q is not valid: %v", s, err))
				continue
			}
			b.URL(url)
		default:
			b.Path(s)
		}
	}
	return b
}

// URL accepts a number of URLs directly.
func (b *Builder) URL(urls ...*url.URL) *Builder {
	for _, u := range urls {
		b.paths = append(b.paths, &URLVisitor{
			Mapper: b.mapper,
			URL:    u,
		})
	}
	return b
}

// Stdin will read objects from the standard input. If ContinueOnError() is set
// prior to this method being called, objects in the stream that are unrecognized
// will be ignored (but logged at V(2)).
func (b *Builder) Stdin() *Builder {
	return b.Stream(os.Stdin, "STDIN")
}

// Stream will read objects from the provided reader, and if an error occurs will
// include the name string in the error message. If ContinueOnError() is set
// prior to this method being called, objects in the stream that are unrecognized
// will be ignored (but logged at V(2)).
func (b *Builder) Stream(r io.Reader, name string) *Builder {
	b.stream = true
	b.paths = append(b.paths, NewStreamVisitor(r, b.mapper, name, b.continueOnError))
	return b
}

// Path is a set of filesystem paths that may be files containing one or more
// resources. If ContinueOnError() is set prior to this method being called,
// objects on the path that are unrecognized will be ignored (but logged at V(2)).
func (b *Builder) Path(paths ...string) *Builder {
	for _, p := range paths {
		i, err := os.Stat(p)
		if os.IsNotExist(err) {
			b.errs = append(b.errs, fmt.Errorf("the path %q does not exist", p))
			continue
		}
		if err != nil {
			b.errs = append(b.errs, fmt.Errorf("the path %q cannot be accessed: %v", p, err))
			continue
		}
		var visitor Visitor
		if i.IsDir() {
			b.dir = true
			visitor = &DirectoryVisitor{
				Mapper:       b.mapper,
				Path:         p,
				Extensions:   []string{".json", ".yaml"},
				Recursive:    false,
				IgnoreErrors: b.continueOnError,
			}
		} else {
			visitor = &PathVisitor{
				Mapper:       b.mapper,
				Path:         p,
				IgnoreErrors: b.continueOnError,
			}
		}
		b.paths = append(b.paths, visitor)
	}
	return b
}

// ResourceTypes is a list of types of resources to operate on, when listing objects on
// the server or retrieving objects that match a selector.
func (b *Builder) ResourceTypes(types ...string) *Builder {
	b.resources = append(b.resources, types...)
	return b
}

// SelectorParam defines a selector that should be applied to the object types to load.
// This will not affect files loaded from disk or URL. If the parameter is empty it is
// a no-op - to select all resources invoke `b.Selector(labels.Everything)`.
func (b *Builder) SelectorParam(s string) *Builder {
	selector, err := labels.Parse(s)
	if err != nil {
		b.errs = append(b.errs, fmt.Errorf("the provided selector %q is not valid: %v", s, err))
		return b
	}
	if selector.Empty() {
		return b
	}
	if b.selectAll {
		b.errs = append(b.errs, fmt.Errorf("found non empty selector %q with previously set 'all' parameter. ", s))
		return b
	}
	return b.Selector(selector)
}

// Selector accepts a selector directly, and if non nil will trigger a list action.
func (b *Builder) Selector(selector labels.Selector) *Builder {
	b.selector = selector
	return b
}

// The namespace that these resources should be assumed to under - used by DefaultNamespace()
// and RequireNamespace()
func (b *Builder) NamespaceParam(namespace string) *Builder {
	b.namespace = namespace
	return b
}

// DefaultNamespace instructs the builder to set the namespace value for any object found
// to NamespaceParam() if empty.
func (b *Builder) DefaultNamespace() *Builder {
	b.defaultNamespace = true
	return b
}

// RequireNamespace instructs the builder to set the namespace value for any object found
// to NamespaceParam() if empty, and if the value on the resource does not match
// NamespaceParam() an error will be returned.
func (b *Builder) RequireNamespace() *Builder {
	b.requireNamespace = true
	return b
}

// SelectEverythingParam
func (b *Builder) SelectAllParam(selectAll bool) *Builder {
	if selectAll && b.selector != nil {
		b.errs = append(b.errs, fmt.Errorf("setting 'all' parameter but found a non empty selector. "))
		return b
	}
	b.selectAll = selectAll
	return b
}

// ResourceTypeOrNameArgs indicates that the builder should accept arguments
// of the form `(<type1>[,<type2>,...]|<type> <name1>[,<name2>,...])`. When one argument is
// received, the types provided will be retrieved from the server (and be comma delimited).
// When two or more arguments are received, they must be a single type and resource name(s).
// The allowEmptySelector permits to select all the resources (via Everything func).
func (b *Builder) ResourceTypeOrNameArgs(allowEmptySelector bool, args ...string) *Builder {
	args = b.replaceAliases(args)
	if ok, err := hasCombinedTypeArgs(args); ok {
		if err != nil {
			b.errs = append(b.errs, err)
			return b
		}
		for _, s := range args {
			seg := strings.Split(s, "/")
			if len(seg) != 2 {
				b.errs = append(b.errs, fmt.Errorf("arguments in resource/name form may not have more than one slash"))
				return b
			}
			resource, name := seg[0], seg[1]
			if len(resource) == 0 || len(name) == 0 || len(SplitResourceArgument(resource)) != 1 {
				b.errs = append(b.errs, fmt.Errorf("arguments in resource/name form must have a single resource and name"))
				return b
			}
			b.resourceTuples = append(b.resourceTuples, resourceTuple{Resource: resource, Name: name})
		}
		return b
	}
	switch {
	case len(args) > 2:
		b.names = append(b.names, args[1:]...)
		b.ResourceTypes(SplitResourceArgument(args[0])...)
	case len(args) == 2:
		b.names = append(b.names, args[1])
		b.ResourceTypes(SplitResourceArgument(args[0])...)
	case len(args) == 1:
		b.ResourceTypes(SplitResourceArgument(args[0])...)
		if b.selector == nil && allowEmptySelector {
			b.selector = labels.Everything()
		}
	case len(args) == 0:
	default:
		b.errs = append(b.errs, fmt.Errorf("when passing arguments, must be resource or resource and name"))
	}
	return b
}

func (b *Builder) replaceAliases(args []string) []string {
	replaced := []string{}
	for _, arg := range args {
		if aliases, ok := b.mapper.AliasesForResource(arg); ok {
			arg = strings.Join(aliases, ",")
		}
		replaced = append(replaced, arg)
	}

	return replaced
}

func hasCombinedTypeArgs(args []string) (bool, error) {
	hasSlash := 0
	for _, s := range args {
		if strings.Contains(s, "/") {
			hasSlash++
		}
	}
	switch {
	case hasSlash > 0 && hasSlash == len(args):
		return true, nil
	case hasSlash > 0 && hasSlash != len(args):
		return true, fmt.Errorf("when passing arguments in resource/name form, all arguments must include the resource")
	default:
		return false, nil
	}
}

// ResourceTypeAndNameArgs expects two arguments, a resource type, and a resource name. The resource
// matching that type and and name will be retrieved from the server.
func (b *Builder) ResourceTypeAndNameArgs(args ...string) *Builder {
	switch len(args) {
	case 2:
		b.names = append(b.names, args[1])
		b.ResourceTypes(SplitResourceArgument(args[0])...)
	case 0:
	default:
		b.errs = append(b.errs, fmt.Errorf("when passing arguments, must be resource and name"))
	}
	return b
}

// Flatten will convert any objects with a field named "Items" that is an array of runtime.Object
// compatible types into individual entries and give them their own items. The original object
// is not passed to any visitors.
func (b *Builder) Flatten() *Builder {
	b.flatten = true
	return b
}

// Latest will fetch the latest copy of any objects loaded from URLs or files from the server.
func (b *Builder) Latest() *Builder {
	b.latest = true
	return b
}

// ContinueOnError will attempt to load and visit as many objects as possible, even if some visits
// return errors or some objects cannot be loaded. The default behavior is to terminate after
// the first error is returned from a VisitorFunc.
func (b *Builder) ContinueOnError() *Builder {
	b.continueOnError = true
	return b
}

// SingleResourceType will cause the builder to error if the user specifies more than a single type
// of resource.
func (b *Builder) SingleResourceType() *Builder {
	b.singleResourceType = true
	return b
}

func (b *Builder) resourceMappings() ([]*meta.RESTMapping, error) {
	if len(b.resources) > 1 && b.singleResourceType {
		return nil, fmt.Errorf("you may only specify a single resource type")
	}
	mappings := []*meta.RESTMapping{}
	for _, r := range b.resources {
		version, kind, err := b.mapper.VersionAndKindForResource(r)
		if err != nil {
			return nil, err
		}
		mapping, err := b.mapper.RESTMapping(kind, version)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, mapping)
	}
	return mappings, nil
}

func (b *Builder) resourceTupleMappings() (map[string]*meta.RESTMapping, error) {
	mappings := make(map[string]*meta.RESTMapping)
	canonical := make(map[string]struct{})
	for _, r := range b.resourceTuples {
		if _, ok := mappings[r.Resource]; ok {
			continue
		}
		version, kind, err := b.mapper.VersionAndKindForResource(r.Resource)
		if err != nil {
			return nil, err
		}
		mapping, err := b.mapper.RESTMapping(kind, version)
		if err != nil {
			return nil, err
		}
		mappings[mapping.Resource] = mapping
		mappings[r.Resource] = mapping
		canonical[mapping.Resource] = struct{}{}
	}
	if len(canonical) > 1 && b.singleResourceType {
		return nil, fmt.Errorf("you may only specify a single resource type")
	}
	return mappings, nil
}

func (b *Builder) visitorResult() *Result {
	if len(b.errs) > 0 {
		return &Result{err: errors.NewAggregate(b.errs)}
	}

	if b.selectAll {
		b.selector = labels.Everything()
	}

	// visit selectors
	if b.selector != nil {
		if len(b.names) != 0 {
			return &Result{err: fmt.Errorf("name cannot be provided when a selector is specified")}
		}
		if len(b.resourceTuples) != 0 {
			return &Result{err: fmt.Errorf("selectors and the all flag cannot be used when passing resource/name arguments")}
		}
		if len(b.resources) == 0 {
			return &Result{err: fmt.Errorf("at least one resource must be specified to use a selector")}
		}
		// empty selector has different error message for paths being provided
		if len(b.paths) != 0 {
			if b.selector.Empty() {
				return &Result{err: fmt.Errorf("when paths, URLs, or stdin is provided as input, you may not specify a resource by arguments as well")}
			} else {
				return &Result{err: fmt.Errorf("a selector may not be specified when path, URL, or stdin is provided as input")}
			}
		}
		mappings, err := b.resourceMappings()
		if err != nil {
			return &Result{err: err}
		}

		visitors := []Visitor{}
		for _, mapping := range mappings {
			client, err := b.mapper.ClientForMapping(mapping)
			if err != nil {
				return &Result{err: err}
			}
			selectorNamespace := b.namespace
			if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
				selectorNamespace = ""
			}
			visitors = append(visitors, NewSelector(client, mapping, selectorNamespace, b.selector))
		}
		if b.continueOnError {
			return &Result{visitor: EagerVisitorList(visitors), sources: visitors}
		}
		return &Result{visitor: VisitorList(visitors), sources: visitors}
	}

	// visit items specified by resource and name
	if len(b.resourceTuples) != 0 {
		isSingular := len(b.resourceTuples) == 1

		if len(b.paths) != 0 {
			return &Result{singular: isSingular, err: fmt.Errorf("when paths, URLs, or stdin is provided as input, you may not specify a resource by arguments as well")}
		}
		if len(b.resources) != 0 {
			return &Result{singular: isSingular, err: fmt.Errorf("you may not specify individual resources and bulk resources in the same call")}
		}

		// retrieve one client for each resource
		mappings, err := b.resourceTupleMappings()
		if err != nil {
			return &Result{singular: isSingular, err: err}
		}
		clients := make(map[string]RESTClient)
		for _, mapping := range mappings {
			s := fmt.Sprintf("%s/%s", mapping.APIVersion, mapping.Resource)
			if _, ok := clients[s]; ok {
				continue
			}
			client, err := b.mapper.ClientForMapping(mapping)
			if err != nil {
				return &Result{err: err}
			}
			clients[s] = client
		}

		items := []Visitor{}
		for _, tuple := range b.resourceTuples {
			mapping, ok := mappings[tuple.Resource]
			if !ok {
				return &Result{singular: isSingular, err: fmt.Errorf("resource %q is not recognized: %v", tuple.Resource, mappings)}
			}
			s := fmt.Sprintf("%s/%s", mapping.APIVersion, mapping.Resource)
			client, ok := clients[s]
			if !ok {
				return &Result{singular: isSingular, err: fmt.Errorf("could not find a client for resource %q", tuple.Resource)}
			}

			selectorNamespace := b.namespace
			if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
				selectorNamespace = ""
			} else {
				if len(b.namespace) == 0 {
					return &Result{singular: isSingular, err: fmt.Errorf("namespace may not be empty when retrieving a resource by name")}
				}
			}

			info := NewInfo(client, mapping, selectorNamespace, tuple.Name)
			items = append(items, info)
		}

		var visitors Visitor
		if b.continueOnError {
			visitors = EagerVisitorList(items)
		} else {
			visitors = VisitorList(items)
		}
		return &Result{singular: isSingular, visitor: visitors, sources: items}
	}

	// visit items specified by name
	if len(b.names) != 0 {
		isSingular := len(b.names) == 1

		if len(b.paths) != 0 {
			return &Result{singular: isSingular, err: fmt.Errorf("when paths, URLs, or stdin is provided as input, you may not specify a resource by arguments as well")}
		}
		if len(b.resources) == 0 {
			return &Result{singular: isSingular, err: fmt.Errorf("you must provide a resource and a resource name together")}
		}
		if len(b.resources) > 1 {
			return &Result{singular: isSingular, err: fmt.Errorf("you must specify only one resource")}
		}

		mappings, err := b.resourceMappings()
		if err != nil {
			return &Result{singular: isSingular, err: err}
		}
		mapping := mappings[0]

		client, err := b.mapper.ClientForMapping(mapping)
		if err != nil {
			return &Result{err: err}
		}

		selectorNamespace := b.namespace
		if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
			selectorNamespace = ""
		} else {
			if len(b.namespace) == 0 {
				return &Result{singular: isSingular, err: fmt.Errorf("namespace may not be empty when retrieving a resource by name")}
			}
		}

		visitors := []Visitor{}
		for _, name := range b.names {
			info := NewInfo(client, mapping, selectorNamespace, name)
			if err := info.Get(); err != nil {
				return &Result{singular: isSingular, err: err}
			}
			visitors = append(visitors, info)
		}
		return &Result{singular: isSingular, visitor: VisitorList(visitors), sources: visitors}
	}

	// visit items specified by paths
	if len(b.paths) != 0 {
		singular := !b.dir && !b.stream && len(b.paths) == 1
		if len(b.resources) != 0 {
			return &Result{singular: singular, err: fmt.Errorf("when paths, URLs, or stdin is provided as input, you may not specify resource arguments as well")}
		}

		var visitors Visitor
		if b.continueOnError {
			visitors = EagerVisitorList(b.paths)
		} else {
			visitors = VisitorList(b.paths)
		}

		// only items from disk can be refetched
		if b.latest {
			// must flatten lists prior to fetching
			if b.flatten {
				visitors = NewFlattenListVisitor(visitors, b.mapper)
			}
			visitors = NewDecoratedVisitor(visitors, RetrieveLatest)
		}
		return &Result{singular: singular, visitor: visitors, sources: b.paths}
	}

	return &Result{err: fmt.Errorf("you must provide one or more resources by argument or filename")}
}

// Do returns a Result object with a Visitor for the resources identified by the Builder.
// The visitor will respect the error behavior specified by ContinueOnError. Note that stream
// inputs are consumed by the first execution - use Infos() or Object() on the Result to capture a list
// for further iteration.
func (b *Builder) Do() *Result {
	r := b.visitorResult()
	if r.err != nil {
		return r
	}
	if b.flatten {
		r.visitor = NewFlattenListVisitor(r.visitor, b.mapper)
	}
	helpers := []VisitorFunc{}
	if b.defaultNamespace {
		helpers = append(helpers, SetNamespace(b.namespace))
	}
	if b.requireNamespace {
		helpers = append(helpers, RequireNamespace(b.namespace))
	}
	helpers = append(helpers, FilterNamespace)
	if b.latest {
		helpers = append(helpers, RetrieveLazy)
	}
	r.visitor = NewDecoratedVisitor(r.visitor, helpers...)
	return r
}

// SplitResourceArgument splits the argument with commas and returns unique
// strings in the original order.
func SplitResourceArgument(arg string) []string {
	out := []string{}
	set := util.NewStringSet()
	for _, s := range strings.Split(arg, ",") {
		if set.Has(s) {
			continue
		}
		set.Insert(s)
		out = append(out, s)
	}
	return out
}