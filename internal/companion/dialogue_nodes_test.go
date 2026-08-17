package companion

import (
	"reflect"
	"testing"
)

func TestSelectProgressStepsSelectsAllSmallPlans(t *testing.T) {
	// 计划长度不超过进展节点上限时必须全选：短计划的每个步骤完成都值得一句
	// 台词，且总请求数仍受 1+n+1 ≤ 8 预算覆盖。
	for stepCount := 0; stepCount <= MaxDialogueProgressNodes; stepCount++ {
		want := make([]int, 0, stepCount)
		for index := 0; index < stepCount; index++ {
			want = append(want, index)
		}
		got := SelectProgressSteps(stepCount)
		// 空集以 nil 或空 slice 表达都合法，比较元素序列而不是 reflect.DeepEqual。
		if len(got) != len(want) {
			t.Fatalf("stepCount=%d: got %v，want %v", stepCount, got, want)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("stepCount=%d: got %v，want %v", stepCount, got, want)
			}
		}
	}
}

func TestSelectProgressStepsEquidistantGolden(t *testing.T) {
	// golden 断言：公式为「把 n 步计划六等分，取每段末尾步骤的完成节点」，
	// 即 1-based 步骤号 floor(i*n/6)（i=1..6），换算 0-based 索引后去重。
	golden := map[int][]int{
		7:    {0, 1, 2, 3, 4, 6},
		12:   {1, 3, 5, 7, 9, 11},
		5000: {832, 1665, 2499, 3332, 4165, 4999},
	}
	for stepCount, want := range golden {
		got := SelectProgressSteps(stepCount)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("stepCount=%d: got %v，want %v", stepCount, got, want)
		}
	}
}

func TestSelectProgressStepsInvariants(t *testing.T) {
	for stepCount := 0; stepCount <= 2000; stepCount++ {
		got := SelectProgressSteps(stepCount)
		if len(got) > MaxDialogueProgressNodes {
			t.Fatalf("stepCount=%d: 选择数 %d 超过上限 %d", stepCount, len(got), MaxDialogueProgressNodes)
		}
		wantLen := stepCount
		if wantLen > MaxDialogueProgressNodes {
			wantLen = MaxDialogueProgressNodes
		}
		if len(got) != wantLen {
			t.Fatalf("stepCount=%d: 选择数 %d，want %d", stepCount, len(got), wantLen)
		}
		previous := -1
		for _, index := range got {
			if index <= previous {
				t.Fatalf("stepCount=%d: 索引 %d 未严格升序去重（previous=%d）", stepCount, index, previous)
			}
			if index < 0 || index >= stepCount {
				t.Fatalf("stepCount=%d: 索引 %d 越界 [0,%d)", stepCount, index, stepCount)
			}
			previous = index
		}
		// 每任务预算推导：开始 1 + 进展 ≤6 + 终态 1 恒不超过冻结预算。
		if total := 1 + len(got) + 1; total > MaxDialogueRequestsPerTask {
			t.Fatalf("stepCount=%d: 总请求数 %d 超过预算 %d", stepCount, total, MaxDialogueRequestsPerTask)
		}
		// 确定性：同一输入重复调用必须得到完全相同的结果。
		if again := SelectProgressSteps(stepCount); !reflect.DeepEqual(got, again) {
			t.Fatalf("stepCount=%d: 重复调用结果漂移 %v vs %v", stepCount, got, again)
		}
	}
	// 负数输入是调用方缺陷，防御性返回空集而不是 panic。
	if got := SelectProgressSteps(-1); len(got) != 0 {
		t.Fatalf("负数 stepCount 必须返回空集，got %v", got)
	}
}

func TestDialogueBudgetConstantsFrozen(t *testing.T) {
	if MaxDialogueRequestsPerTask != 8 {
		t.Fatalf("MaxDialogueRequestsPerTask = %d，want 8", MaxDialogueRequestsPerTask)
	}
	if MaxDialogueProgressNodes != 6 {
		t.Fatalf("MaxDialogueProgressNodes = %d，want 6", MaxDialogueProgressNodes)
	}
	if FollowDialogueNodeCount != 3 {
		t.Fatalf("FollowDialogueNodeCount = %d，want 3", FollowDialogueNodeCount)
	}
}

func TestDialogueNodeValidateMatrix(t *testing.T) {
	cases := map[string]struct {
		node DialogueNode
		want bool
	}{
		"开始节点零载荷":       {DialogueNode{Kind: DialogueNodeStart}, true},
		"开始节点携带步骤类型":    {DialogueNode{Kind: DialogueNodeStart, StepKind: PlanStepGoTo}, false},
		"开始节点携带终态":      {DialogueNode{Kind: DialogueNodeStart, State: TaskCompleted}, false},
		"进展节点携带采掘步骤":    {DialogueNode{Kind: DialogueNodeProgress, StepKind: PlanStepMine}, true},
		"进展节点携带放置步骤":    {DialogueNode{Kind: DialogueNodeProgress, StepKind: PlanStepPlace}, true},
		"进展节点缺步骤类型":     {DialogueNode{Kind: DialogueNodeProgress}, false},
		"进展节点携带 follow": {DialogueNode{Kind: DialogueNodeProgress, StepKind: PlanStepFollow}, false},
		"进展节点携带终态":      {DialogueNode{Kind: DialogueNodeProgress, StepKind: PlanStepGoTo, State: TaskRunning}, false},
		"首达节点零载荷":       {DialogueNode{Kind: DialogueNodeFirstArrival}, true},
		"首达节点携带步骤类型":    {DialogueNode{Kind: DialogueNodeFirstArrival, StepKind: PlanStepGoTo}, false},
		"首达节点携带终态":      {DialogueNode{Kind: DialogueNodeFirstArrival, State: TaskCompleted}, false},
		"完成终态":          {DialogueNode{Kind: DialogueNodeTerminal, State: TaskCompleted}, true},
		"失败终态携带稳定原因":    {DialogueNode{Kind: DialogueNodeTerminal, State: TaskFailed, Reason: TaskFailPathUnreachable}, true},
		"失败终态缺稳定原因":     {DialogueNode{Kind: DialogueNodeTerminal, State: TaskFailed}, false},
		"超时终态":          {DialogueNode{Kind: DialogueNodeTerminal, State: TaskTimedOut}, true},
		"停止终态":          {DialogueNode{Kind: DialogueNodeTerminal, State: TaskStopped}, true},
		"终态携带非终态":       {DialogueNode{Kind: DialogueNodeTerminal, State: TaskRunning}, false},
		"终态携带步骤类型":      {DialogueNode{Kind: DialogueNodeTerminal, State: TaskCompleted, StepKind: PlanStepGoTo}, false},
		"非失败终态携带原因":     {DialogueNode{Kind: DialogueNodeTerminal, State: TaskStopped, Reason: TaskFailPathUnreachable}, false},
		"零值节点":          {DialogueNode{}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.node.Validate()
			if tc.want && err != nil {
				t.Fatalf("合法节点被拒绝: %v", err)
			}
			if !tc.want && err == nil {
				t.Fatalf("非法节点 %+v 被接受", tc.node)
			}
		})
	}
}
