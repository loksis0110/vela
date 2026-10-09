// Command rules compiles plain domain and CIDR lists into a sing-box binary
// rule set (.srs). Used by scripts/update-rules.sh.
//
//	go run ./tools/rules -out core/rules/ru-inside.srs -domains a.lst -cidrs b.lst,c.lst
package main

import (
	"bufio"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"

	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

func readLines(files string) []string {
	var out []string
	for _, f := range strings.Split(files, ",") {
		if f == "" {
			continue
		}
		file, err := os.Open(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		sc := bufio.NewScanner(file)
		for sc.Scan() {
			l := strings.TrimSpace(sc.Text())
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			out = append(out, l)
		}
		file.Close()
	}
	return out
}

func main() {
	out := flag.String("out", "", "output .srs file")
	domains := flag.String("domains", "", "comma-separated domain list files")
	cidrs := flag.String("cidrs", "", "comma-separated CIDR list files")
	flag.Parse()
	if *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	seen := map[string]bool{}
	var suffixes []string
	for _, d := range readLines(*domains) {
		d = strings.ToLower(strings.TrimPrefix(d, "."))
		if d != "" && !seen[d] {
			seen[d] = true
			suffixes = append(suffixes, d)
		}
	}
	var prefixes []string
	for _, c := range readLines(*cidrs) {
		if p, err := netip.ParsePrefix(c); err == nil && !seen[p.String()] {
			seen[p.String()] = true
			prefixes = append(prefixes, p.String())
		} else if a, err := netip.ParseAddr(c); err == nil {
			prefixes = append(prefixes, netip.PrefixFrom(a, a.BitLen()).String())
		}
	}
	sort.Strings(suffixes)
	sort.Strings(prefixes)
	var rules []option.HeadlessRule
	if len(suffixes) > 0 {
		rules = append(rules, option.HeadlessRule{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultHeadlessRule{
			DomainSuffix: badoption.Listable[string](suffixes)}})
	}
	if len(prefixes) > 0 {
		rules = append(rules, option.HeadlessRule{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultHeadlessRule{
			IPCIDR: badoption.Listable[string](prefixes)}})
	}
	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := srs.Write(f, option.PlainRuleSet{Rules: rules}, C.RuleSetVersion3); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	f.Close()
	fmt.Printf("%s: %d domains, %d networks\n", *out, len(suffixes), len(prefixes))
}
