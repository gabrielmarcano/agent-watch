import Foundation

// A simple wrapper to safely decode dynamic dictionaries like tool_input
struct AnyCodable: Codable {
    let value: Any
    
    init(value: Any) {
        self.value = value
    }
    
    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let stringVal = try? container.decode(String.self) {
            value = stringVal
        } else if let intVal = try? container.decode(Int.self) {
            value = intVal
        } else if let doubleVal = try? container.decode(Double.self) {
            value = doubleVal
        } else if let boolVal = try? container.decode(Bool.self) {
            value = boolVal
        } else if let arrayVal = try? container.decode([AnyCodable].self) {
            value = arrayVal.map { $0.value }
        } else if let dictVal = try? container.decode([String: AnyCodable].self) {
            value = dictVal.mapValues { $0.value }
        } else {
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "AnyCodable value cannot be decoded")
        }
    }
    
    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch value {
        case let string as String:
            try container.encode(string)
        case let int as Int:
            try container.encode(int)
        case let double as Double:
            try container.encode(double)
        case let bool as Bool:
            try container.encode(bool)
        case let array as [Any]:
            try container.encode(array.map { AnyCodable(value: $0) })
        case let dict as [String: Any]:
            try container.encode(dict.mapValues { AnyCodable(value: $0) })
        default:
            throw EncodingError.invalidValue(value, EncodingError.Context(codingPath: container.codingPath, debugDescription: "AnyCodable value cannot be encoded"))
        }
    }
}

struct AgentState: Codable {
    var status: String = "idle" // "idle" | "thinking" | "waiting_for_permission" | "done"
    var session_id: String?
    var name: String?
    var title: String? // /v1 AgentState.title (contracts.md §1.2); unused by the legacy client until Phase 6
    var workspace: String?
    var cwd: String?
    var last_query: String?
    var last_response: String?
    var history: [HistoryItem] = []
    var tool_name: String?
    var tool_input: [String: AnyCodable]?
    var timestamp: String?
    
    // Helper to get command string easily for the UI
    var commandToApprove: String? {
        if let input = tool_input {
            if let cmd = input["command"]?.value as? String {
                return cmd
            }
            if let file = input["file_path"]?.value as? String {
                return file
            }
        }
        return nil
    }
}

// Body of POST /v1/agents/{pane_id}/cancel (contracts.md §2.2).
// fingerprint is the prompt the watch showed; nil is omitted from the JSON.
struct CancelRequest: Codable, Sendable {
    let expected_seq: UInt64
    let fingerprint: String?
}

// One option of a prompt (contracts.md §1.3); the legacy client does not show /v1 prompts yet (Phase 6).
// description holds the lines printed under the label; nil when the JSON omits it.
struct PromptOption: Codable, Sendable {
    let id: String
    let label: String
    let description: String?
    let role: String
}
