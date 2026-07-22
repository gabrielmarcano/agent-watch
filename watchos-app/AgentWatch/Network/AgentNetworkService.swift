import Foundation
import Combine

class AgentNetworkService: NSObject, ObservableObject, URLSessionDataDelegate {
    @Published var state: AgentState = AgentState()
    
    @Published var localIp: String {
        didSet { UserDefaults.standard.set(localIp, forKey: "local_ip") }
    }
    @Published var tailscaleIp: String {
        didSet { UserDefaults.standard.set(tailscaleIp, forKey: "tailscale_ip") }
    }
    
    private var currentIp: String
    private var usingFallback = false
    private let port = 8420
    
    private var session: URLSession!
    private var dataTask: URLSessionDataTask?
    
    override init() {
        let defaultLocalIp = UserDefaults.standard.string(forKey: "local_ip") ?? "192.168.1.20"
        self.localIp = defaultLocalIp
        self.tailscaleIp = UserDefaults.standard.string(forKey: "tailscale_ip") ?? "100.64.0.1"
        self.currentIp = defaultLocalIp
        
        super.init()
        
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 10 * 60 // 10 minutes for SSE
        config.timeoutIntervalForResource = 10 * 60
        self.session = URLSession(configuration: config, delegate: self, delegateQueue: nil)
    }
    
    func startListening() {
        stopListening()
        
        let urlString = "http://\(currentIp):\(port)/events"
        guard let url = URL(string: urlString) else { return }
        
        print("Connecting to SSE: \(url)")
        let request = URLRequest(url: url)
        
        dataTask = session.dataTask(with: request)
        dataTask?.resume()
    }
    
    func stopListening() {
        dataTask?.cancel()
        dataTask = nil
    }
    
    private func reconnect(delay: TimeInterval = 3.0) {
        stopListening()
        DispatchQueue.main.asyncAfter(deadline: .now() + delay) { [weak self] in
            self?.startListening()
        }
    }
    
    // MARK: - URLSessionDataDelegate for SSE Parsing
    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        // Simple SSE parsing
        guard let string = String(data: data, encoding: .utf8) else { return }
        let lines = string.components(separatedBy: "\n")
        
        var isStateEvent = false
        
        for line in lines {
            if line.hasPrefix("event: state") {
                isStateEvent = true
            } else if line.hasPrefix("data: ") && isStateEvent {
                let jsonString = line.dropFirst(6)
                if let jsonData = jsonString.data(using: .utf8) {
                    do {
                        let newState = try JSONDecoder().decode(AgentState.self, from: jsonData)
                        DispatchQueue.main.async {
                            self.state = newState
                        }
                    } catch {
                        print("Error parsing state JSON: \(error)")
                    }
                }
                isStateEvent = false
            }
        }
    }
    
    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        print("SSE Connection Closed/Failed: \(error?.localizedDescription ?? "No error")")
        
        if currentIp == localIp && !usingFallback {
            print("Switching to Tailscale fallback: \(tailscaleIp)")
            currentIp = tailscaleIp
            usingFallback = true
            startListening()
        } else {
            print("Connection failed for both. Retrying local in 3s")
            currentIp = localIp
            usingFallback = false
            reconnect()
        }
    }
    
    // MARK: - Upstream Commands
    func sendInputCommand(text: String, completion: @escaping (Bool) -> Void = { _ in }) {
        let urlString = "http://\(currentIp):\(port)/input"
        guard let url = URL(string: urlString) else { return }
        
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        
        let payload = ["text": text]
        request.httpBody = try? JSONSerialization.data(withJSONObject: payload)
        
        URLSession.shared.dataTask(with: request) { data, response, error in
            let success = (response as? HTTPURLResponse)?.statusCode == 200 && error == nil
            completion(success)
        }.resume()
    }
    
    func registerPushToken(_ token: String) {
        let urlString = "http://\(currentIp):\(port)/register"
        guard let url = URL(string: urlString) else { return }
        
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        
        let payload = ["token": token]
        request.httpBody = try? JSONSerialization.data(withJSONObject: payload)
        
        URLSession.shared.dataTask(with: request).resume()
    }
}
