// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
)

type UsageMoney int64

func (m UsageMoney) MarshalJSON() ([]byte, error) {
	return json.Marshal(pricing.FormatCost(int64(m)))
}

func (m *UsageMoney) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("usage money: %w", err)
	}
	negative := strings.HasPrefix(text, "-")
	whole, frac, dotted := strings.Cut(strings.TrimPrefix(text, "-"), ".")
	if whole == "" || (dotted && frac == "") || len(frac) > pricing.CostDecimalPlaces || strings.ContainsAny(whole+frac, "+-") {
		return fmt.Errorf("usage money: %q is not a decimal amount", text)
	}
	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return fmt.Errorf("usage money: %q: %w", text, err)
	}
	fraction, err := strconv.ParseInt(frac+strings.Repeat("0", pricing.CostDecimalPlaces-len(frac)), 10, 64)
	if err != nil {
		return fmt.Errorf("usage money: %q: %w", text, err)
	}
	if units > (math.MaxInt64-fraction)/usageMoneyUnit {
		return fmt.Errorf("usage money: %q is too large", text)
	}
	v := units*usageMoneyUnit + fraction
	if negative {
		v = -v
	}
	*m = UsageMoney(v)
	return nil
}

const usageMoneyUnit = 10_000_000_000

type UsageScope string

const (
	UsageScopeTenant UsageScope = "tenant"
	UsageScopeSelf   UsageScope = "self"
)

type UsageWindow struct {
	StartTime RESTTimestamp `json:"startTime"`
	EndTime   RESTTimestamp `json:"endTime"`
}

type UsageCostByBucket struct {
	UncachedInput UsageMoney `json:"uncachedInput"`
	Output        UsageMoney `json:"output"`
	CacheRead     UsageMoney `json:"cacheRead"`
	CacheWrite    UsageMoney `json:"cacheWrite"`
}

type UsageTokens struct {
	UncachedInput int64 `json:"uncachedInput"`
	Output        int64 `json:"output"`
	CacheRead     int64 `json:"cacheRead"`
	CacheWrite    int64 `json:"cacheWrite"`
}

type UsageTotals struct {
	Cost          UsageMoney        `json:"cost"`
	CostByBucket  UsageCostByBucket `json:"costByBucket"`
	Tokens        UsageTokens       `json:"tokens"`
	Calls         int64             `json:"calls"`
	UnpricedCalls int64             `json:"unpricedCalls"`
	Activities    int64             `json:"activities"`
}

type UsageSubjectTotals struct {
	Cost            UsageMoney        `json:"cost"`
	Activities      int64             `json:"activities"`
	CostPerActivity *UsageMoney       `json:"costPerActivity"`
	ShareOfSpend    *float64          `json:"shareOfSpend"`
	CostByBucket    UsageCostByBucket `json:"costByBucket"`
	Tokens          UsageTokens       `json:"tokens"`
	Calls           int64             `json:"calls"`
	UnpricedCalls   int64             `json:"unpricedCalls"`
}

type UsageDay struct {
	Date       string      `json:"date"`
	Cost       UsageMoney  `json:"cost"`
	Tokens     UsageTokens `json:"tokens"`
	Activities int64       `json:"activities"`
}

type UsageComparison struct {
	Cost          UsageMoney `json:"cost"`
	ChangePercent *float64   `json:"changePercent"`
}

type UsageTopUp struct {
	ID          string        `json:"id"`
	Amount      UsageMoney    `json:"amount"`
	ExpiresTime RESTTimestamp `json:"expiresTime"`
}

type UsageBudgetStatus string

const (
	UsageBudgetOK          UsageBudgetStatus = "ok"
	UsageBudgetApproaching UsageBudgetStatus = "approaching"
	UsageBudgetExceeded    UsageBudgetStatus = "exceeded"
	UsageBudgetUnlimited   UsageBudgetStatus = "unlimited"
)

type UsageBudgetSource string

const (
	UsageBudgetAssigned UsageBudgetSource = "assigned"
	UsageBudgetDefault  UsageBudgetSource = "default"
)

type UsageBudget struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Source            UsageBudgetSource `json:"source"`
	Amount            *UsageMoney       `json:"amount"`
	WarningPercentage *int              `json:"warningPercentage"`
	TopUp             *UsageTopUp       `json:"topUp"`
	EffectiveAmount   *UsageMoney       `json:"effectiveAmount"`
	PeriodStart       RESTTimestamp     `json:"periodStart"`
	PeriodEnd         RESTTimestamp     `json:"periodEnd"`
	PeriodSpend       UsageMoney        `json:"periodSpend"`
	UsagePercent      *float64          `json:"usagePercent"`
	Status            UsageBudgetStatus `json:"status"`
}

type UsageBudgetWindow struct {
	WindowBudget UsageMoney `json:"windowBudget"`
	UsagePercent float64    `json:"usagePercent"`
}

type UsageUser struct {
	Email        *string `json:"email"`
	IsSystemUser bool    `json:"isSystemUser"`
}

type UsageSummaryResponse struct {
	Scope            UsageScope       `json:"scope"`
	DataAsOf         *RESTTimestamp   `json:"dataAsOf"`
	HistoryComplete  bool             `json:"historyComplete"`
	Window           UsageWindow      `json:"window"`
	ComparisonWindow *UsageWindow     `json:"comparisonWindow"`
	Currency         string           `json:"currency"`
	Totals           UsageTotals      `json:"totals"`
	CostByKind       UsageCostByKind  `json:"costByKind"`
	Comparison       *UsageComparison `json:"comparison"`
	MeshBudget       *UsageBudget     `json:"meshBudget"`
	Daily            []UsageDay       `json:"daily"`
}

type UsageCostByKind struct {
	Users     UsageMoney `json:"users"`
	Agents    UsageMoney `json:"agents"`
	Workflows UsageMoney `json:"workflows"`
	Models    UsageMoney `json:"models"`
}

type UsageRowResponse struct {
	ID              *string           `json:"id"`
	Name            *string           `json:"name"`
	BookedName      *string           `json:"bookedName"`
	Cost            UsageMoney        `json:"cost"`
	Activities      int64             `json:"activities"`
	CostPerActivity *UsageMoney       `json:"costPerActivity"`
	ShareOfSpend    *float64          `json:"shareOfSpend"`
	CostByBucket    UsageCostByBucket `json:"costByBucket"`
	Tokens          UsageTokens       `json:"tokens"`
	Calls           int64             `json:"calls"`
	UnpricedCalls   int64             `json:"unpricedCalls"`
	Daily           []UsageDay        `json:"daily"`
}

type UsageServedModel struct {
	Name          *string           `json:"name"`
	Provider      *string           `json:"provider"`
	Cost          UsageMoney        `json:"cost"`
	Activities    int64             `json:"activities"`
	CostByBucket  UsageCostByBucket `json:"costByBucket"`
	Tokens        UsageTokens       `json:"tokens"`
	Calls         int64             `json:"calls"`
	UnpricedCalls int64             `json:"unpricedCalls"`
}

type UsageModelRowResponse struct {
	UsageRowResponse
	Models []UsageServedModel `json:"models"`
}

type UsageUserRowResponse struct {
	UsageRowResponse
	User        *UsageUser         `json:"user"`
	Budget      *UsageBudget       `json:"budget"`
	BudgetUsage *UsageBudgetWindow `json:"budgetUsage"`
}

type UsageSubjectResponse struct {
	Scope           UsageScope         `json:"scope"`
	DataAsOf        *RESTTimestamp     `json:"dataAsOf"`
	HistoryComplete bool               `json:"historyComplete"`
	ID              *string            `json:"id"`
	Name            string             `json:"name"`
	BookedName      *string            `json:"bookedName"`
	Window          UsageWindow        `json:"window"`
	Totals          UsageSubjectTotals `json:"totals"`
	Daily           []UsageDay         `json:"daily"`
}

type UsageNodeKind string

const (
	UsageNodeAgent    UsageNodeKind = "agent"
	UsageNodeWorkflow UsageNodeKind = "workflow"
	UsageNodeTool     UsageNodeKind = "tool"
	UsageNodeSwitch   UsageNodeKind = "switch"
	UsageNodeLoop     UsageNodeKind = "loop"
	UsageNodeMap      UsageNodeKind = "map"
	UsageNodeDecision UsageNodeKind = "decision"
)

type UsageModelResponse struct {
	UsageSubjectResponse
	Models []UsageServedModel `json:"models"`
}

type UsageWorkflowNode struct {
	NodeID          string         `json:"nodeId"`
	Name            string         `json:"name"`
	Kind            *UsageNodeKind `json:"kind"`
	Cost            UsageMoney     `json:"cost"`
	ShareOfWorkflow float64        `json:"shareOfWorkflow"`
	ChangePercent   *float64       `json:"changePercent"`
}

type UsageWorkflowResponse struct {
	Scope            UsageScope          `json:"scope"`
	DataAsOf         *RESTTimestamp      `json:"dataAsOf"`
	HistoryComplete  bool                `json:"historyComplete"`
	ID               *string             `json:"id"`
	Name             string              `json:"name"`
	BookedName       *string             `json:"bookedName"`
	Window           UsageWindow         `json:"window"`
	ComparisonWindow *UsageWindow        `json:"comparisonWindow"`
	Totals           UsageSubjectTotals  `json:"totals"`
	Daily            []UsageDay          `json:"daily"`
	Nodes            []UsageWorkflowNode `json:"nodes"`
}

type UsageRootKind string

const (
	UsageRootAgent    UsageRootKind = "agent"
	UsageRootWorkflow UsageRootKind = "workflow"
)

type UsageRoot struct {
	Kind        UsageRootKind `json:"kind"`
	ID          *string       `json:"id"`
	Name        string        `json:"name"`
	BookedName  *string       `json:"bookedName"`
	Cost        UsageMoney    `json:"cost"`
	ShareOfUser float64       `json:"shareOfUser"`
}

type UsageUserResponse struct {
	Scope            UsageScope         `json:"scope"`
	DataAsOf         *RESTTimestamp     `json:"dataAsOf"`
	HistoryComplete  bool               `json:"historyComplete"`
	ID               *string            `json:"id"`
	Name             *string            `json:"name"`
	User             *UsageUser         `json:"user"`
	Window           UsageWindow        `json:"window"`
	Totals           UsageSubjectTotals `json:"totals"`
	Daily            []UsageDay         `json:"daily"`
	Budget           *UsageBudget       `json:"budget"`
	TopRoots         []UsageRoot        `json:"topRoots"`
	OtherCost        UsageMoney         `json:"otherCost"`
	OtherShareOfUser float64            `json:"otherShareOfUser"`
}

type UsageMonth struct {
	Month           string      `json:"month"`
	Cost            UsageMoney  `json:"cost"`
	BudgetAmount    *UsageMoney `json:"budgetAmount"`
	PartialFromDate *string     `json:"partialFromDate"`
}

type UsageSavingsResponse struct {
	Scope              UsageScope     `json:"scope"`
	DataAsOf           *RESTTimestamp `json:"dataAsOf"`
	HistoryComplete    bool           `json:"historyComplete"`
	Window             UsageWindow    `json:"window"`
	Currency           string         `json:"currency"`
	AvoidedInputTokens *int64         `json:"avoidedInputTokens"`
	AvoidedCost        *UsageMoney    `json:"avoidedCost"`
	Calls              int64          `json:"calls"`
	MeasuredCalls      int64          `json:"measuredCalls"`
	UnpricedCalls      int64          `json:"unpricedCalls"`
}

type UsageSelfResponse struct {
	UsageUserResponse
	Months []UsageMonth `json:"months"`
}
