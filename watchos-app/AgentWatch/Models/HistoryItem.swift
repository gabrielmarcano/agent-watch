import Foundation

struct HistoryItem: Codable, Identifiable, Hashable {
    var id: String
    var query: String?
    var response: String?
    var timestamp: String?
    var host: String? // /v1 HistoryItem.host (contracts.md §1.4); unused until Phase 6
    
    // For manual instantiation when needed
    init(id: String = UUID().uuidString, query: String? = nil, response: String? = nil, timestamp: String? = nil) {
        self.id = id
        self.query = query
        self.response = response
        self.timestamp = timestamp
    }
}
