// AlgoForge deterministic test-data primitives. C++20; no third-party headers.
// Tree/graph helpers guarantee structure, not uniform sampling over all graphs.
// Problem-specific constraints need an independent validator.
#ifndef ALGOFORGE_TESTDATA_V1_HPP
#define ALGOFORGE_TESTDATA_V1_HPP
#include <algorithm>
#include <cstdint>
#include <iostream>
#include <limits>
#include <numeric>
#include <stdexcept>
#include <string>
#include <unordered_set>
#include <utility>
#include <vector>
namespace af {
using i64 = long long;
using u64 = std::uint64_t;
using u128 = unsigned __int128;
using Edge = std::pair<int, int>;
inline constexpr int max_items = 2000000;
inline void require(bool ok, const std::string& reason) {
    if (!ok) throw std::invalid_argument(reason);
}
inline void size_ok(int n) { require(n >= 0 && n <= max_items, "size must be in [0,2000000]"); }
class Random {
    u64 state_;
public:
    explicit Random(u64 seed) : state_(seed) {}
    u64 next() {
        u64 z = (state_ += 0x9e3779b97f4a7c15ULL);
        z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9ULL;
        z = (z ^ (z >> 27)) * 0x94d049bb133111ebULL;
        return z ^ (z >> 31);
    }
    u64 below(u128 n) {
        require(n > 0 && n <= (u128(1) << 64), "invalid random range");
        if (n == (u128(1) << 64)) return next();
        const u64 bound = static_cast<u64>(n), threshold = -bound % bound;
        for (int attempt = 0; attempt < 128; ++attempt) {
            u64 x = next();
            if (x >= threshold) return x % bound;
        }
        throw std::runtime_error("random sampling attempt limit exceeded");
    }
    i64 integer(i64 lo, i64 hi) {
        require(lo <= hi, "integer lower bound exceeds upper bound");
        return static_cast<i64>(__int128(lo) + below(u128(__int128(hi) - lo) + 1));
    }
    template<class T> void shuffle(std::vector<T>& a) {
        require(a.size() <= max_items, "shuffle size limit exceeded");
        for (std::size_t i = a.size(); i > 1; --i) std::swap(a[i-1], a[below(i)]);
    }
    // Floyd sampling: expected O(k) time/space, independent of range size.
    std::vector<u64> sample(u128 range, int k) {
        size_ok(k);
        require(range <= (u128(1) << 64) && u128(k) <= range, "sample exceeds range");
        std::unordered_set<u64> used;
        used.reserve(static_cast<std::size_t>(k) * 2 + 1);
        std::vector<u64> a; a.reserve(k);
        for (int i = 0; i < k; ++i) {
            u128 j = range - k + i;
            u64 x = below(j + 1);
            if (used.count(x)) x = static_cast<u64>(j);
            used.insert(x); a.push_back(x);
        }
        shuffle(a);
        return a;
    }
    std::vector<i64> array(int n, i64 lo, i64 hi) {
        size_ok(n); require(lo <= hi, "invalid array bounds");
        std::vector<i64> a(n);
        for (auto& x : a) x = integer(lo, hi);
        return a;
    }
    std::vector<i64> distinct(int n, i64 lo, i64 hi) {
        size_ok(n); require(lo <= hi, "invalid distinct bounds");
        auto ids = sample(u128(__int128(hi) - lo) + 1, n);
        std::vector<i64> a; a.reserve(n);
        for (auto id : ids) a.push_back(static_cast<i64>(__int128(lo) + id));
        return a;
    }
    std::vector<int> permutation(int n) {
        size_ok(n); std::vector<int> a(n);
        std::iota(a.begin(), a.end(), 1); shuffle(a); return a;
    }
    std::string text(int n, const std::string& alphabet = "abcdefghijklmnopqrstuvwxyz") {
        size_ok(n); require(!alphabet.empty(), "alphabet must not be empty");
        std::string s(n, ' ');
        for (auto& c : s) c = alphabet[below(alphabet.size())];
        return s;
    }
    std::string palindrome(int n, const std::string& alphabet = "abcdefghijklmnopqrstuvwxyz") {
        auto s = text(n, alphabet);
        for (int i = 0; i < n/2; ++i) s[n-1-i] = s[i];
        return s;
    }
    // Root is 1. binary means at most two children INCLUDING the root.
    std::vector<Edge> tree(int n, const std::string& shape = "random") {
        size_ok(n); require(n >= 1, "tree needs at least one node");
        require(shape=="random" || shape=="chain" || shape=="star" || shape=="binary",
                "unknown tree shape");
        std::vector<Edge> edges; edges.reserve(n-1);
        std::vector<int> available{1}, children(n+1, 0);
        for (int v = 2; v <= n; ++v) {
            int u = 1;
            if (shape == "chain") u = v-1;
            if (shape == "random") u = static_cast<int>(integer(1, v-1));
            if (shape == "binary") {
                auto pos = below(available.size()); u = available[pos];
                if (++children[u] == 2) {
                    available[pos] = available.back(); available.pop_back();
                }
                available.push_back(v);
            }
            edges.emplace_back(u, v);
        }
        return edges;
    }
    std::vector<Edge> degree_tree(int n, int degree) {
        size_ok(n); require(n >= 1 && degree >= 1, "invalid degree tree parameters");
        require(n <= 2 || degree >= 2, "degree one cannot connect more than two nodes");
        std::vector<Edge> edges; edges.reserve(n-1);
        std::vector<int> available{1}, deg(n+1, 0);
        for (int v = 2; v <= n; ++v) {
            auto pos = below(available.size()); int u = available[pos];
            edges.emplace_back(u, v);
            if (++deg[u] == degree) {
                available[pos] = available.back(); available.pop_back();
            }
            deg[v] = 1;
            if (degree > 1) available.push_back(v);
        }
        return edges;
    }
    // Triangular IDs and sampling without replacement also handle dense graphs.
    static u64 row_start(int n, int u) { return u64(u-1) * (2ULL*n-u) / 2; }
    static Edge edge_from_id(int n, u64 id) {
        int lo=1, hi=n-1;
        while (lo < hi) {
            int mid=lo+(hi-lo+1)/2;
            if (row_start(n,mid) <= id) lo=mid; else hi=mid-1;
        }
        return {lo, lo+1+static_cast<int>(id-row_start(n,lo))};
    }
    std::vector<Edge> graph(int n, int m, bool connected = false) {
        size_ok(n); size_ok(m); require(n >= 1, "graph needs at least one node");
        u64 total=u64(n)*(n-1)/2;
        require(u64(m) <= total && (!connected || m >= n-1), "impossible edge count");
        std::vector<Edge> edges;
        std::vector<u64> blocked;
        if (connected) {
            edges=tree(n);
            for (auto [u,v] : edges) blocked.push_back(row_start(n,u)+v-u-1);
            std::sort(blocked.begin(), blocked.end());
        }
        auto chosen=sample(total-blocked.size(), m-static_cast<int>(edges.size()));
        for (u64 rank : chosen) {
            u64 lo=rank, hi=rank+blocked.size();
            while (lo < hi) {
                u64 mid=lo+(hi-lo)/2;
                u64 excluded=std::upper_bound(blocked.begin(),blocked.end(),mid)-blocked.begin();
                if (mid+1-excluded >= rank+1) hi=mid; else lo=mid+1;
            }
            edges.push_back(edge_from_id(n,lo));
        }
        shuffle(edges); return edges;
    }
    std::vector<Edge> dag(int n, int m) {
        auto edges=graph(n,m);
        auto labels=permutation(n);
        for (auto& [u,v] : edges) { u=labels[u-1]; v=labels[v-1]; }
        shuffle(edges); return edges;
    }
    // Relabelling changes root identity; apply after root-specific construction.
    void relabel(int n, std::vector<Edge>& edges) {
        auto labels=permutation(n);
        for (auto& [u,v] : edges) {
            require(u>=1 && u<=n && v>=1 && v<=n, "edge endpoint out of range");
            u=labels[u-1]; v=labels[v-1];
        }
        shuffle(edges);
    }
};
template<class T> void line(std::ostream& out, const std::vector<T>& a) {
    for (std::size_t i=0; i<a.size(); ++i) { if (i) out << ' '; out << a[i]; }
    out << '\n';
}
inline void print_edges(std::ostream& out, const std::vector<Edge>& edges) {
    for (auto [u,v] : edges) out << u << ' ' << v << '\n';
}
} // namespace af
#endif
