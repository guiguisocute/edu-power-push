/*
Package provider 定义上游电表数据契约。
核心仅依赖本包接口；具体爬虫在子目录 Register。
新增学校实现 Provider + Querier 即可，无需改核心。
*/
package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

/*
Querier 为一所学校的查询能力。
三方法独立；不支持的返回带 Errors 的空结果，禁止 panic。
*/
type Querier interface {
	QueryMeter(ctx context.Context, meter string, opts QueryOptions) Result
	QueryMonthlyBills(ctx context.Context, meter string, months []string, opts MonthlyQueryOptions) MonthlyBillsResult
	QueryDailyDetails(ctx context.Context, meter string, months []string, opts DailyDetailQueryOptions) DailyDetailsResult
}

/*
School 描述可选学校及实测支持层。
面板据此灰显不可用的账单/日明细采集。
*/
type School struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Balance bool   `json:"balance"`
	Bill    bool   `json:"bill"`
	Detail  bool   `json:"detail"`
}

// SchoolConfig 为打开 Querier 的输入。
type SchoolConfig struct {
	AreaID   string
	AreaName string
	BaseURL  *url.URL
	Timeout  time.Duration
}

/*
Provider 为一套上游协议，可挂多所学校。
Schools 空切片表示无清单，面板退化为输入框。
*/
type Provider interface {
	// Name 为注册名，亦为 PROVIDER 取值，必须稳定小写。
	Name() string
	// DisplayName 为面板展示名。
	DisplayName() string
	// Schools 列出已知学校；无清单返回空切片。
	Schools() []School
	// DefaultBaseURL 在未配 ELECTRICITY_BASE_URL 时使用；空串表示必填。
	DefaultBaseURL() string
	// Open 打开查询器。参数非法时返回错误，禁止 nil,nil。
	Open(SchoolConfig) (Querier, error)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Provider{}
)

/*
Register 登记上游实现，通常在 init 中调用。
重名 panic，禁止静默覆盖。
*/
func Register(p Provider) {
	if p == nil {
		panic("provider: Register(nil)")
	}
	name := strings.TrimSpace(p.Name())
	if name == "" {
		panic("provider: Name() must not be empty")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic("provider: duplicate registration for " + name)
	}
	registry[name] = p
}

func Get(name string) (Provider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[strings.TrimSpace(name)]
	return p, ok
}

// List 按名排序返回已注册实现，供面板展示。
func List() []Provider {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Provider, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

/*
Resolve 按名取实现。
名为空且仅一套时自动选用；多套留空则报错。
*/
func Resolve(name string) (Provider, error) {
	if strings.TrimSpace(name) != "" {
		p, ok := Get(name)
		if !ok {
			return nil, fmt.Errorf("unknown provider %q (registered: %s)", name, strings.Join(Names(), ", "))
		}
		return p, nil
	}
	all := List()
	switch len(all) {
	case 0:
		return nil, errors.New("no upstream provider is registered")
	case 1:
		return all[0], nil
	default:
		return nil, fmt.Errorf("PROVIDER must be set; registered: %s", strings.Join(Names(), ", "))
	}
}

// Names 返回已注册名，排序稳定。
func Names() []string {
	out := []string{}
	for _, p := range List() {
		out = append(out, p.Name())
	}
	return out
}

// ErrSchoolNotConfigured：未选学校。采集器据此跳过本轮。
var ErrSchoolNotConfigured = errors.New("school is not configured")

/*
Dynamic 为运行时可替换的 Querier。
原子换指针：一轮内学校不变，换挡在两轮之间。
未配置时返回 ErrSchoolNotConfigured，禁止空指针崩溃。
*/
type Dynamic struct {
	mu      sync.RWMutex
	querier Querier
	school  SchoolConfig
}

func NewDynamic() *Dynamic { return &Dynamic{} }

// Set 替换查询器；querier 为 nil 表示未配置。
func (d *Dynamic) Set(querier Querier, school SchoolConfig) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.querier, d.school = querier, school
}

// Current 返回当前查询器与学校；未配置时 querier 为 nil。
func (d *Dynamic) Current() (Querier, SchoolConfig) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.querier, d.school
}

func (d *Dynamic) School() SchoolConfig {
	_, school := d.Current()
	return school
}

func (d *Dynamic) Ready() bool {
	querier, _ := d.Current()
	return querier != nil
}

func (d *Dynamic) QueryMeter(ctx context.Context, meter string, opts QueryOptions) Result {
	querier, school := d.Current()
	if querier == nil {
		return Result{
			QueriedAt: time.Now(), Meter: meter, Status: StatusError,
			Errors: []string{ErrSchoolNotConfigured.Error()},
		}
	}
	result := querier.QueryMeter(ctx, meter, opts)
	/* 上游未回填学校时补全，保证多校数据可分。 */
	if result.AreaID == "" {
		result.AreaID, result.AreaName = school.AreaID, school.AreaName
	}
	return result
}

func (d *Dynamic) QueryMonthlyBills(ctx context.Context, meter string, months []string, opts MonthlyQueryOptions) MonthlyBillsResult {
	querier, _ := d.Current()
	if querier == nil {
		return MonthlyBillsResult{
			QueriedAt: time.Now(), Meter: meter, Status: MonthlyStatusError,
			MonthsRequested: len(months), Bills: []MonthlyBillMonth{},
			Errors: []string{ErrSchoolNotConfigured.Error()},
		}
	}
	return querier.QueryMonthlyBills(ctx, meter, months, opts)
}

func (d *Dynamic) QueryDailyDetails(ctx context.Context, meter string, months []string, opts DailyDetailQueryOptions) DailyDetailsResult {
	querier, _ := d.Current()
	if querier == nil {
		return DailyDetailsResult{
			QueriedAt: time.Now(), Meter: meter, Status: DailyDetailStatusError,
			MonthsRequested: len(months), Months: []DailyDetailMonth{},
			Errors: []string{ErrSchoolNotConfigured.Error()},
		}
	}
	return querier.QueryDailyDetails(ctx, meter, months, opts)
}
