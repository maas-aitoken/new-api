package billingexpr

import (
	"fmt"
	"math"

	"github.com/expr-lang/expr/ast"
)

// UsesDurPricing reports whether an expression calls dur(). Hosts use it to
// reject the function on entrances whose conversion is not the token $/1M
// scale (task usage expressions settle raw USD, so dur()'s internal 1e6
// scaling would overcharge by a million).
func UsesDurPricing(expression string) bool {
	return UsesDurPricingByHash(expression, ExprHashString(expression))
}

// UsesDurPricingByHash avoids hashing an expression already in a snapshot.
func UsesDurPricingByHash(expression, hash string) bool {
	entry, err := compileEntryFromCacheByHash(expression, hash)
	return err == nil && entry.usesDur
}

// validateDurCalls enforces the dur(seconds, usd_per_minute) shape: exactly
// two arguments with a finite, non-negative numeric literal unit price. The
// seconds argument may be any numeric expression (typically audio_sec /
// video_sec); non-negative saturation happens at settlement.
func validateDurCalls(root ast.Node) error {
	var err error
	ast.Find(root, func(n ast.Node) bool {
		call, ok := n.(*ast.CallNode)
		if !ok {
			return false
		}
		callee, ok := call.Callee.(*ast.IdentifierNode)
		if !ok || callee.Value != "dur" {
			return false
		}
		if len(call.Arguments) != 2 {
			err = fmt.Errorf("dur(seconds, usd_per_minute) expects exactly 2 arguments")
			return true
		}
		price, literal := requestRuleNumber(call.Arguments[1])
		if !literal || price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			err = fmt.Errorf("dur() unit price must be a finite, non-negative numeric literal")
			return true
		}
		return false
	})
	return err
}
