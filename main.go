package main

import (
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"os"

	"github.com/golang/protobuf/proto"
	clientmodel "github.com/prometheus/client_golang/model"
	"github.com/prometheus/log"
	"gopkg.in/yaml.v2"

	"github.com/prometheus/migrate/v0x13"
	"github.com/prometheus/migrate/v0x14"
)

var outName = flag.String("out", "-", "Target for writing the output")

func usage() {
	fmt.Fprintf(os.Stderr, "usage: %s [args ...] [<config_file>]", flag.Arg(0))

	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	flag.Parse()

	var (
		err error
		in  io.Reader = os.Stdin
		out io.Writer = os.Stdout
	)

	if flag.NArg() > 0 {
		filename := flag.Args()[0]
		in, err = os.Open(filename)
		if err != nil {
			log.Fatalf("Error opening input file: %s", err)
		}
		log.Infof("Translating file %s", filename)
	}

	if err := translate(in, out); err != nil {
		log.Fatal(err)
	}
}

func translate(in io.Reader, out io.Writer) error {
	b, err := ioutil.ReadAll(in)
	if err != nil {
		return err
	}
	var oldConf v0x13.Config
	err = proto.UnmarshalText(string(b), &oldConf.PrometheusConfig)
	if err != nil {
		return fmt.Errorf("Error parsing old config file: %s", err)
	}

	var newGlobConf v0x14.GlobalConfig

	newGlobConf.ScrapeInterval = v0x14.Duration(oldConf.ScrapeInterval())
	// The global scrape timeout is new and will be set to the global scrape interval.
	newGlobConf.ScrapeTimeout = newGlobConf.ScrapeInterval
	newGlobConf.EvaluationInterval = v0x14.Duration(oldConf.EvaluationInterval())

	var newConf v0x14.Config

	newConf.GlobalConfig = &newGlobConf
	if oldConf.Global != nil {
		newConf.RuleFiles = oldConf.Global.GetRuleFile()
	}

	var scrapeConfs []*v0x14.ScrapeConfig
	for _, oldJob := range oldConf.Jobs() {
		scfg := &v0x14.ScrapeConfig{}

		scfg.JobName = oldJob.GetName()

		var firstScheme string
		var firstPath string
		for _, oldTG := range oldJob.TargetGroup {
			newTG := &v0x14.TargetGroup{
				Labels: clientmodel.LabelSet{},
			}

			for _, t := range oldTG.Target {
				u, err := url.Parse(t)
				if err != nil {
					return err
				}

				if firstScheme == "" {
					firstScheme = u.Scheme
				} else if u.Scheme != firstScheme {
					return fmt.Errorf("Multiple URL schemes in Job not allowed.")
				}
				if firstPath == "" {
					firstPath = u.Path
				} else if u.Path != firstPath {
					return fmt.Errorf("Multiple paths in Job not allowed")
				}

				newTG.Targets = append(newTG.Targets, clientmodel.LabelSet{
					clientmodel.AddressLabel: clientmodel.LabelValue(u.Host),
				})
			}

			for _, lp := range oldTG.GetLabels().GetLabel() {
				ln := clientmodel.LabelName(lp.GetName())
				lv := clientmodel.LabelValue(lp.GetValue())
				newTG.Labels[ln] = lv
			}
			scfg.TargetGroups = append(scfg.TargetGroups, newTG)
		}
		scfg.Scheme = firstScheme

		if oldJob.SdName != nil {
			dnscfg := &v0x14.DNSConfig{}

			dnscfg.Names = []string{*oldJob.SdName}
			dnscfg.RefreshInterval = v0x14.Duration(oldJob.SDRefreshInterval())

			scfg.DNSConfigs = append(scfg.DNSConfigs, dnscfg)
		}

		scrapeConfs = append(scrapeConfs, scfg)
	}

	newConf.ScrapeConfigs = scrapeConfs

	res, err := yaml.Marshal(newConf)
	if err != nil {
		return err
	}

	if _, err := out.Write(res); err != nil {
		return err
	}
	return nil
}
