package productsearch

import (
	"context"
	"sort"
	"sync"

	"smartinsure-eino-backend/internal/platform"
)

type Service struct {
	Platforms []platform.Platform
}

type Options struct {
	MaxPerPlatform int
	MaxTotal       int
}

func New(platforms ...platform.Platform) *Service {
	if len(platforms) == 0 {
		platforms = DefaultPlatforms()
	}
	return &Service{Platforms: platforms}
}

func DefaultPlatforms() []platform.Platform {
	return []platform.Platform{
		platform.Xiaoyusan{},
		platform.Pingan{},
		platform.Huize{},
	}
}

func (s *Service) Search(ctx context.Context, userInput string, opts Options) ([]platform.ProductCard, error) {
	if len(s.Platforms) == 0 {
		return []platform.ProductCard{}, nil
	}
	if opts.MaxPerPlatform <= 0 {
		opts.MaxPerPlatform = 10
	}
	if opts.MaxTotal <= 0 {
		opts.MaxTotal = 10
	}

	keywords := platform.ExtractKeywords(userInput)
	if len(keywords) > 3 {
		keywords = keywords[:3]
	}
	budget, hasBudget := ExtractBudget(userInput)

	results := s.searchConcurrent(ctx, keywords, opts.MaxPerPlatform)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	products := interleaveAndDedupe(results)
	products = FilterIrrelevant(products, keywords)
	products = FilterByAge(products, userInput)

	isFamily := false
	if hasBudget {
		if familySize := DetectFamilySize(userInput); familySize > 1 {
			budget = budget / float64(familySize)
			isFamily = true
		}
		lowerRatio := 0.1
		if isFamily {
			lowerRatio = 0.05
		}
		products = FilterByBudget(products, budget, 0.5, lowerRatio)
	}

	if len(products) > opts.MaxTotal {
		products = products[:opts.MaxTotal]
	}
	return products, nil
}

func (s *Service) searchConcurrent(ctx context.Context, keywords []string, limit int) [][]platform.ProductCard {
	type result struct {
		index int
		items []platform.ProductCard
	}
	total := len(s.Platforms) * len(keywords)
	out := make([][]platform.ProductCard, total)
	ch := make(chan result, total)
	var wg sync.WaitGroup

	idx := 0
	for _, p := range s.Platforms {
		for _, kw := range keywords {
			currentIndex := idx
			currentPlatform := p
			currentKeyword := kw
			wg.Add(1)
			go func() {
				defer wg.Done()
				items, err := currentPlatform.Search(ctx, currentKeyword, 1)
				if err != nil {
					ch <- result{index: currentIndex}
					return
				}
				if len(items) > limit {
					items = items[:limit]
				}
				ch <- result{index: currentIndex, items: items}
			}()
			idx++
		}
	}

	wg.Wait()
	close(ch)
	for res := range ch {
		out[res.index] = res.items
	}
	return out
}

func interleaveAndDedupe(results [][]platform.ProductCard) []platform.ProductCard {
	maxRounds := 0
	for _, items := range results {
		if len(items) > maxRounds {
			maxRounds = len(items)
		}
	}
	seen := map[string]bool{}
	products := make([]platform.ProductCard, 0)
	for i := 0; i < maxRounds; i++ {
		for _, items := range results {
			if i >= len(items) {
				continue
			}
			p := items[i]
			key := p.Name
			if key == "" {
				key = p.ID
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			products = append(products, p)
		}
	}
	return products
}

func sortByBudgetScore(scored []budgetScore) {
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score < scored[j].score
	})
}
