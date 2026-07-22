import Foundation

struct HistoryItem: Codable, Identifiable, Hashable {
    var id: String
    var query: String?
    var response: String?
    var timestamp: String?
    
    // For manual instantiation when needed
    init(id: String = UUID().uuidString, query: String? = nil, response: String? = nil, timestamp: String? = nil) {
        self.id = id
        self.query = query
        self.response = response
        self.timestamp = timestamp
    }
}
