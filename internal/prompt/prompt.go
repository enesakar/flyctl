// Package prompt implements input-related functionality.
package prompt

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/samber/lo"
	"github.com/superfly/flyctl/iostreams"

	"github.com/superfly/flyctl/api"
	"github.com/superfly/flyctl/client"
	"github.com/superfly/flyctl/internal/config"
	"github.com/superfly/flyctl/internal/sort"
)

func String(ctx context.Context, dst *string, msg, def string, required bool) error {
	opt, err := newSurveyIO(ctx)
	if err != nil {
		return err
	}

	p := &survey.Input{
		Message: msg,
		Default: def,
	}

	opts := []survey.AskOpt{opt}
	if required {
		opts = append(opts, survey.WithValidator(survey.Required))
	}

	return survey.AskOne(p, dst, opts...)
}

func Int(ctx context.Context, dst *int, msg string, def int, required bool) error {
	opt, err := newSurveyIO(ctx)
	if err != nil {
		return err
	}

	p := &survey.Input{
		Message: msg,
		Default: strconv.Itoa(def),
	}

	opts := []survey.AskOpt{opt}
	if required {
		opts = append(opts, survey.WithValidator(survey.Required))
	}
	// add a validator to ensure that the input is an integer
	opts = append(opts, survey.WithValidator(func(val interface{}) error {
		_, err := strconv.Atoi(val.(string))
		if err != nil {
			return errors.New("must be an integer")
		}
		return nil
	}))
	return survey.AskOne(p, dst, opts...)
}

func Password(ctx context.Context, dst *string, msg string, required bool) error {
	opt, err := newSurveyIO(ctx)
	if err != nil {
		return err
	}

	p := &survey.Password{
		Message: msg,
	}

	opts := []survey.AskOpt{opt}
	if required {
		opts = append(opts, survey.WithValidator(survey.Required))
	}

	return survey.AskOne(p, dst, opts...)
}

func MultiSelect(ctx context.Context, indices *[]int, msg string, def []int, options ...string) error {
	opt, err := newSurveyIO(ctx)
	if err != nil {
		return err
	}

	p := &survey.MultiSelect{
		Message:  msg,
		Options:  options,
		PageSize: 15,
		Default:  def,
	}

	return survey.AskOne(p, indices, opt)
}

func Select(ctx context.Context, index *int, msg, def string, options ...string) error {
	opt, err := newSurveyIO(ctx)
	if err != nil {
		return err
	}

	p := &survey.Select{
		Message:  msg,
		Options:  options,
		PageSize: 15,
	}

	if def != "" {
		p.Default = def
	}

	return survey.AskOne(p, index, opt)
}

func Confirmf(ctx context.Context, format string, a ...interface{}) (bool, error) {
	return Confirm(ctx, fmt.Sprintf(format, a...))
}

func Confirm(ctx context.Context, message string) (confirm bool, err error) {
	var opt survey.AskOpt
	if opt, err = newSurveyIO(ctx); err != nil {
		return
	}

	prompt := &survey.Confirm{
		Message: message,
	}

	err = survey.AskOne(prompt, &confirm, opt)

	return
}

func ConfirmOverwrite(ctx context.Context, filename string) (confirm bool, err error) {
	prompt := &survey.Confirm{
		Message: fmt.Sprintf(`Overwrite "%s"?`, filename),
	}
	err = survey.AskOne(prompt, &confirm)

	return
}

var errNonInteractive = errors.New("prompt: non interactive")

func IsNonInteractive(err error) bool {
	return errors.Is(err, errNonInteractive)
}

type NonInteractiveError string

func (e NonInteractiveError) Error() string { return string(e) }

func (NonInteractiveError) Unwrap() error { return errNonInteractive }

func newSurveyIO(ctx context.Context) (survey.AskOpt, error) {
	io := iostreams.FromContext(ctx)
	if !io.IsInteractive() {
		return nil, errNonInteractive
	}

	in, ok := io.In.(terminal.FileReader)
	if !ok {
		return nil, errNonInteractive
	}

	out, ok := io.Out.(terminal.FileWriter)
	if !ok {
		return nil, errNonInteractive
	}

	return survey.WithStdio(in, out, io.ErrOut), nil
}

var errOrgSlugRequired = NonInteractiveError("org slug must be specified when not running interactively")

// Org returns the Organization the user has passed in via flag or prompts the
// user for one.
func Org(ctx context.Context) (*api.Organization, error) {
	client := client.FromContext(ctx).API()

	orgs, err := client.GetOrganizations(ctx)
	if err != nil {
		return nil, err
	}
	sort.OrganizationsByTypeAndName(orgs)

	io := iostreams.FromContext(ctx)
	slug := config.FromContext(ctx).Organization

	switch {
	case slug == "" && len(orgs) == 1 && orgs[0].Type == "PERSONAL":
		fmt.Fprintf(io.ErrOut, "automatically selected %s organization: %s\n",
			strings.ToLower(orgs[0].Type), orgs[0].Name)

		return &orgs[0], nil
	case slug != "":
		for _, org := range orgs {
			if slug == org.Slug {
				return &org, nil
			}
		}

		return nil, fmt.Errorf("organization %s not found", slug)
	default:
		switch org, err := SelectOrg(ctx, orgs); {
		case err == nil:
			return org, nil
		case IsNonInteractive(err):
			return nil, errOrgSlugRequired
		default:
			return nil, err
		}
	}
}

func SelectOrg(ctx context.Context, orgs []api.Organization) (org *api.Organization, err error) {
	var options []string
	for _, org := range orgs {
		personalCallout := ""
		if org.Type == "PERSONAL" && org.Slug != "personal" {
			personalCallout = " [personal]"
		}
		options = append(options, fmt.Sprintf("%s (%s)%s", org.Name, org.Slug, personalCallout))
	}

	var index int
	if err = Select(ctx, &index, "Select Organization:", "", options...); err == nil {
		org = &orgs[index]
	}

	return
}

var (
	errRegionCodeRequired  = NonInteractiveError("region code must be specified when not running interactively")
	errRegionCodesRequired = NonInteractiveError("regions codes must be specified in a comma-separated when not running interactively")
)

func sortedRegions(ctx context.Context) ([]api.Region, *api.Region, error) {
	client := client.FromContext(ctx).API()

	regions, defaultRegion, err := client.PlatformRegions(ctx)
	if err != nil {
		return nil, nil, err
	}

	sort.RegionsByNameAndCode(regions)

	return regions, defaultRegion, err
}

// Region returns the region the user has passed in via flag or prompts the
// user for one.
func MultiRegion(ctx context.Context, msg string, currentRegions []string, excludeRegion string) (*[]api.Region, error) {
	regions, _, err := sortedRegions(ctx)
	if err != nil {
		return nil, err
	}

	switch regions, err := MultiSelectRegion(ctx, msg, regions, currentRegions, excludeRegion); {
	case err == nil:
		return &regions, nil
	case IsNonInteractive(err):
		return nil, errRegionCodesRequired
	default:
		return nil, err
	}
}

// Region returns the region the user has passed in via flag or prompts the
// user for one.
func Region(ctx context.Context, msg string) (*api.Region, error) {
	regions, defaultRegion, err := sortedRegions(ctx)
	if err != nil {
		return nil, err
	}

	slug := config.FromContext(ctx).Region

	switch {
	case slug != "":
		for _, region := range regions {
			if slug == region.Code {
				return &region, nil
			}
		}

		return nil, fmt.Errorf("region %s not found", slug)
	default:
		var defaultRegionCode string
		if defaultRegion != nil {
			defaultRegionCode = defaultRegion.Code
		}

		switch region, err := SelectRegion(ctx, msg, regions, defaultRegionCode); {
		case err == nil:
			return region, nil
		case IsNonInteractive(err):
			return nil, errRegionCodeRequired
		default:
			return nil, err
		}
	}
}

func SelectRegion(ctx context.Context, msg string, regions []api.Region, defaultCode string) (region *api.Region, err error) {
	var defaultOption string

	var options []string
	for _, r := range regions {
		option := fmt.Sprintf("%s (%s)", r.Name, r.Code)
		if r.Code == defaultCode {
			defaultOption = option
		}

		options = append(options, option)
	}

	if msg == "" {
		msg = "Select regions:"
	}

	var index int
	if err = Select(ctx, &index, msg, defaultOption, options...); err == nil {
		region = &regions[index]
	}

	return
}

func MultiSelectRegion(ctx context.Context, msg string, regions []api.Region, currentRegions []string, excludeRegion string) (selectedRegions []api.Region, err error) {
	var options []string

	includedRegions := lo.Filter(regions, func(r api.Region, _ int) bool {
		return r.Code != excludeRegion
	})

	var currentIndices []int
	var indices []int

	for i, r := range includedRegions {
		if lo.Contains(currentRegions, r.Code) {
			currentIndices = append(currentIndices, i)
		}
		option := fmt.Sprintf("%s (%s)", r.Name, r.Code)
		options = append(options, option)
	}

	if msg == "" {
		msg = "Select regions:"
	}

	if err = MultiSelect(ctx, &indices, msg, currentIndices, options...); err == nil {
		for _, index := range indices {
			selectedRegions = append(selectedRegions, includedRegions[index])
		}
	}
	return
}

var errVMsizeRequired = NonInteractiveError("vm size must be specified when not running interactively")

func VMSize(ctx context.Context, def string) (size *api.VMSize, err error) {
	client := client.FromContext(ctx).API()

	vmSizes, err := client.PlatformVMSizes(ctx)
	if err != nil {
		return nil, err
	}

	sort.VMSizesBySize(vmSizes)

	switch {
	case def != "":
		for _, vmSize := range vmSizes {
			if def == vmSize.Name {
				return &vmSize, nil
			}
		}
		return nil, fmt.Errorf("vm size %s not found", def)
	default:
		switch vmSize, err := SelectVMSize(ctx, vmSizes); {
		case err == nil:
			return vmSize, nil
		case IsNonInteractive(err):
			return nil, errVMsizeRequired
		default:
			return nil, err
		}
	}
}

func SelectVMSize(ctx context.Context, vmSizes []api.VMSize) (vmSize *api.VMSize, err error) {
	options := []string{}

	for _, vmSize := range vmSizes {
		options = append(options, fmt.Sprintf("%s - %d", vmSize.Name, vmSize.MemoryMB))
	}

	var index int

	if err := Select(ctx, &index, "Select VM size:", "", options...); err != nil {
		return nil, err
	}
	return &vmSizes[index], nil
}
