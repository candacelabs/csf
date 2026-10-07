// Copyright 2026 Candace Labs

package costs

import (
	"maps"
	"math"
	"slices"
)

// Price is one model's dollars per token of each kind.
type Price struct {
	Uncached float64
	Write    float64
	Read     float64
	Output   float64
}

// Of is what the tokens cost at this price.
func (price Price) Of(tokens Tokens) float64 {
	return float64(tokens.Uncached)*price.Uncached + float64(tokens.Write)*price.Write +
		float64(tokens.Read)*price.Read + float64(tokens.Output)*price.Output
}

// kinds is the number of token kinds a price has.
const kinds = 4

// uncachedKind is the index of uncached input in a price vector.
const uncachedKind = 0

// identifiableShare is the least share of a model's input tokens a kind
// needs before the fit estimates its price: below it the kind's column is
// numerically indistinguishable from noise in the dollars, and it moves no
// money either way.
const identifiableShare = 1e-3

// PriceFit is one model's fitted price with its 95% interval per kind.
type PriceFit struct {
	Model string
	Price Price
	// Low and High bound each kind's price, in the order of Price's fields.
	Low  [kinds]float64
	High [kinds]float64
	// N is the turns the fit read; Residual is the root mean square of the
	// dollars it does not explain.
	N        int
	Residual float64
	// Determined is false for a kind no turn of the model used.
	Determined [kinds]bool
}

// normalQuantile is the standard normal's 0.975 quantile.
const normalQuantile = 1.959963984540054

// FitPrices fits each model's price per token kind by least squares to the
// money the record reports per model per turn, which the endpoint computed
// from the same four counts. A kind no turn used is left undetermined at
// zero.
func FitPrices(runs []Run) []PriceFit {
	observations := map[string][]Priced{}
	for _, run := range runs {
		for _, turn := range run.Turns {
			for model, priced := range turn.ByModel {
				if priced.Tokens.Input()+priced.Tokens.Output > 0 {
					observations[model] = append(observations[model], priced)
				}
			}
		}
	}
	fits := []PriceFit{}
	for _, model := range slices.Sorted(maps.Keys(observations)) {
		fits = append(fits, fitModel(model, observations[model]))
	}
	return fits
}

// fitModel solves the normal equations over the kinds the model used, in
// millions of tokens so the system stays well conditioned.
func fitModel(model string, observations []Priced) PriceFit {
	fit := PriceFit{Model: model, N: len(observations)}
	rows := make([][kinds]float64, len(observations))
	var uncached, input int64
	for index, observation := range observations {
		tokens := observation.Tokens
		rows[index] = [kinds]float64{float64(tokens.Uncached) / 1e6, float64(tokens.Write) / 1e6, float64(tokens.Read) / 1e6, float64(tokens.Output) / 1e6}
		for kind := range kinds {
			fit.Determined[kind] = fit.Determined[kind] || rows[index][kind] > 0
		}
		uncached, input = uncached+tokens.Uncached, input+tokens.Input()
	}
	if input == 0 || float64(uncached)/float64(input) < identifiableShare {
		fit.Determined[uncachedKind] = false
	}
	used := []int{}
	for kind := range kinds {
		if fit.Determined[kind] {
			used = append(used, kind)
		}
	}
	size := len(used)
	if size == 0 || len(observations) < size {
		fit.Determined = [kinds]bool{}
		return fit
	}
	values := make([]float64, len(observations))
	for index, observation := range observations {
		values[index] = observation.USD
	}
	solution, inverse, ok := leastSquares(rows, used, values)
	if !ok {
		fit.Determined = [kinds]bool{}
		return fit
	}
	var squares float64
	for index, row := range rows {
		predicted := 0.0
		for left := range size {
			predicted += row[used[left]] * solution[left]
		}
		squares += math.Pow(observations[index].USD-predicted, 2)
	}
	fit.Residual = math.Sqrt(squares / float64(len(observations)))
	variance := 0.0
	if degrees := len(observations) - size; degrees > 0 {
		variance = squares / float64(degrees)
	}
	perMillion := [kinds]float64{}
	for left, kind := range used {
		perMillion[kind] = solution[left]
		spread := normalQuantile * math.Sqrt(variance*math.Max(inverse[left][left], 0))
		fit.Low[kind], fit.High[kind] = (solution[left]-spread)/1e6, (solution[left]+spread)/1e6
	}
	fit.Price = Price{Uncached: perMillion[0] / 1e6, Write: perMillion[1] / 1e6, Read: perMillion[2] / 1e6, Output: perMillion[3] / 1e6}
	return fit
}

// leastSquares solves the normal equations of rows, restricted to the used
// columns, against values: the solution and the inverse of the normal
// matrix its intervals come from; false when the system is singular.
func leastSquares(rows [][kinds]float64, used []int, values []float64) ([]float64, [][]float64, bool) {
	size := len(used)
	normal := make([][]float64, size)
	right := make([]float64, size)
	for row := range size {
		normal[row] = make([]float64, size)
	}
	for index, row := range rows {
		for left := range size {
			right[left] += row[used[left]] * values[index]
			for other := range size {
				normal[left][other] += row[used[left]] * row[used[other]]
			}
		}
	}
	inverse, ok := invert(normal)
	if !ok {
		return nil, nil, false
	}
	solution := make([]float64, size)
	for left := range size {
		for other := range size {
			solution[left] += inverse[left][other] * right[other]
		}
	}
	return solution, inverse, true
}

// invert inverts a square matrix by Gauss-Jordan elimination with partial
// pivoting; false when it is singular.
func invert(matrix [][]float64) ([][]float64, bool) {
	size := len(matrix)
	work := make([][]float64, size)
	for row := range size {
		work[row] = make([]float64, 2*size)
		copy(work[row], matrix[row])
		work[row][size+row] = 1
	}
	for column := range size {
		pivot := column
		for row := column + 1; row < size; row++ {
			if math.Abs(work[row][column]) > math.Abs(work[pivot][column]) {
				pivot = row
			}
		}
		if math.Abs(work[pivot][column]) < 1e-12 {
			return nil, false
		}
		work[column], work[pivot] = work[pivot], work[column]
		scale := work[column][column]
		for index := range work[column] {
			work[column][index] /= scale
		}
		for row := range size {
			if row == column {
				continue
			}
			factor := work[row][column]
			for index := range work[row] {
				work[row][index] -= factor * work[column][index]
			}
		}
	}
	inverse := make([][]float64, size)
	for row := range size {
		inverse[row] = work[row][size:]
	}
	return inverse, true
}

// Prices is the fitted price of every model.
func Prices(fits []PriceFit) map[string]Price {
	prices := map[string]Price{}
	for _, fit := range fits {
		prices[fit.Model] = fit.Price
	}
	return prices
}
