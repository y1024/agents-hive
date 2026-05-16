package agentquality

type Case struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Route          string      `json:"route"`
	Input          string      `json:"input"`
	ExpectedTools  []string    `json:"expected_tools,omitempty"`
	AllowedTools   []string    `json:"allowed_tools,omitempty"`
	ExpectedSkills []string    `json:"expected_skills,omitempty"`
	ExpectedAgents []string    `json:"expected_agents,omitempty"`
	Scenario       string      `json:"scenario,omitempty"`
	ExpectedStatus FinalStatus `json:"expected_status"`
	FailureType    FailureType `json:"failure_type,omitempty"`
	Risk           string      `json:"risk,omitempty"`
	Required       bool        `json:"required"`
	Notes          string      `json:"notes,omitempty"`

	// Phase 3: Extended fields for golden case lifecycle
	DomainID         string            `json:"domain_id,omitempty"`
	SourceKind       string            `json:"source_kind,omitempty"`
	SourceName       string            `json:"source_name,omitempty"`
	ExpectedAnswer   string            `json:"expected_answer,omitempty"`
	JudgeRubric      []RubricCriterion `json:"judge_rubric,omitempty"`
	Assertions       []CaseAssertion   `json:"assertions,omitempty"`
	EvidenceLevelMin string            `json:"evidence_level_min,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	CreatedFrom      string            `json:"created_from,omitempty"`
	State            string            `json:"state,omitempty"`
}
