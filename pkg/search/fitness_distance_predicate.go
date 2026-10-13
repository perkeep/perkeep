/*
Copyright 2026 The Perkeep Authors

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

package search

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"perkeep.org/pkg/schema/nodeattr"
)

const fitnessDistanceNumber = `(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]{1,3})?`

var (
	fitnessDistanceValueExpr = regexp.MustCompile(`^` + fitnessDistanceNumber + `$`)
	fitnessDistanceRangeExpr = regexp.MustCompile(`^(` + fitnessDistanceNumber + `)?\s*-\s*(` + fitnessDistanceNumber + `)?$`)
)

type fitnessDistance struct {
	matchPrefix
	numerator, denominator int64
}

func newFitnessDistance(name string, numerator, denominator int64) keyword {
	return fitnessDistance{newMatchPrefix(name), numerator, denominator}
}

func (k fitnessDistance) Description() string {
	alias := ""
	if k.Name() == "kilometers" {
		alias = "alias for km; "
	}
	return alias + "matches fitness activity distance in " + k.Name() +
		"; use >=5, <=5, >5, <5, =5 or 5, and inclusive ranges 5-10, 5-, -10." +
		" Values must be nonnegative finite decimal numbers (scientific notation allowed); miles are 1609.344 meters."
}

// meters rounds only after exact decimal unit conversion. Multiplying two
// float64 values instead can round twice, missing e.g. miles:3 at 4828.032 m.
func (k fitnessDistance) meters(value string) (float64, *big.Rat, error) {
	value = strings.TrimSpace(value)
	if !fitnessDistanceValueExpr.MatchString(value) {
		return 0, nil, fmt.Errorf("invalid distance %q", value)
	}
	if i := strings.IndexAny(value, "eE"); i >= 0 {
		exponent, _ := strconv.Atoi(value[i+1:])
		if exponent < -400 || exponent > 400 {
			return 0, nil, fmt.Errorf("distance exponent out of range")
		}
	}
	if _, err := strconv.ParseFloat(value, 64); err != nil {
		return 0, nil, fmt.Errorf("invalid distance %q: %w", value, err)
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok {
		return 0, nil, fmt.Errorf("invalid distance %q", value)
	}
	r.Mul(r, big.NewRat(k.numerator, k.denominator))
	v, _ := r.Float64()
	if math.IsInf(v, 0) || math.IsNaN(v) || (v == 0 && r.Sign() != 0) {
		return 0, nil, fmt.Errorf("distance %q is outside the finite meter range", value)
	}
	return v, r, nil
}

func distanceFloatConstraint(fc *FloatConstraint) *Constraint {
	return &Constraint{Permanode: &PermanodeConstraint{
		Attr: nodeattr.FitnessDistance, ValueMatchesFloat: fc, SkipHidden: true,
	}}
}

func (k fitnessDistance) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	value := strings.TrimSpace(args[0])
	if len(value) > 128 {
		return nil, fmt.Errorf("distance expression is too long")
	}
	op := ""
	for _, prefix := range []string{">=", "<=", "=", ">", "<"} {
		if strings.HasPrefix(value, prefix) {
			op, value = prefix, strings.TrimSpace(strings.TrimPrefix(value, prefix))
			break
		}
	}
	fc := &FloatConstraint{ZeroMin: true}
	var c *Constraint
	if bounds := fitnessDistanceRangeExpr.FindStringSubmatch(value); op == "" && bounds != nil {
		if bounds[1] == "" && bounds[2] == "" {
			return nil, fmt.Errorf("distance range needs at least one bound")
		}
		var lower, upper *big.Rat
		var err error
		if bounds[1] != "" {
			fc.Min, lower, err = k.meters(bounds[1])
			if err != nil {
				return nil, err
			}
			fc.ZeroMin = fc.Min == 0
		}
		if bounds[2] != "" {
			fc.Max, upper, err = k.meters(bounds[2])
			if err != nil {
				return nil, err
			}
			fc.ZeroMax = fc.Max == 0
		}
		if lower != nil && upper != nil && lower.Cmp(upper) > 0 {
			return nil, fmt.Errorf("distance range minimum is greater than maximum")
		}
		c = distanceFloatConstraint(fc)
	} else {
		v, _, err := k.meters(value)
		if err != nil {
			return nil, err
		}
		switch op {
		case ">=":
			fc.Min, fc.ZeroMin = v, v == 0
		case "<=":
			fc.Max, fc.ZeroMax = v, v == 0
		case ">", "<":
			// Keep a finite, nonnegative distance requirement outside NOT:
			// otherwise missing, invalid or negative distances would match.
			opposite := &FloatConstraint{}
			if op == ">" {
				opposite.Max, opposite.ZeroMax = v, v == 0
			} else {
				opposite.Min, opposite.ZeroMin = v, v == 0
			}
			c = andConst(distanceFloatConstraint(fc), notConst(distanceFloatConstraint(opposite)))
		default: // Bare numbers and "=" are exact.
			fc.Min, fc.Max, fc.ZeroMin, fc.ZeroMax = v, v, v == 0, v == 0
		}
		if c == nil {
			c = distanceFloatConstraint(fc)
		}
	}
	return andConst(fitnessActivityConstraint(""), c), nil
}
