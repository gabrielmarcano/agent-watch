import SwiftUI

struct HistoryListView: View {
    var state: AgentState
    
    var historyList: [HistoryItem] {
        if !state.history.isEmpty {
            return state.history.reversed()
        } else if let resp = state.last_response, !resp.isEmpty {
            return [HistoryItem(id: "latest", query: state.last_query, response: resp, timestamp: state.timestamp)]
        }
        return []
    }
    
    var body: some View {
        if historyList.isEmpty {
            Text("No history available")
                .foregroundColor(.gray)
                .font(.system(size: 12))
        } else {
            List(historyList) { item in
                NavigationLink(destination: ReaderDetailView(item: item)) {
                    VStack(alignment: .leading, spacing: 4) {
                        if let query = item.query, !query.isEmpty {
                            Text("Q: \(query)")
                                .font(.system(size: 11, weight: .medium))
                                .foregroundColor(.blue)
                                .lineLimit(1)
                        }
                        
                        Text(MarkdownFormatter.truncate(item.response, maxChars: 120))
                            .font(.system(size: 12))
                            .foregroundColor(.white.opacity(0.9))
                            .lineLimit(4)
                        
                        Text("READ FULL ->")
                            .font(.system(size: 9, weight: .bold))
                            .foregroundColor(.blue)
                    }
                    .padding(.vertical, 4)
                }
            }
            .navigationTitle("HISTORY")
            .navigationBarTitleDisplayMode(.inline)
        }
    }
}
