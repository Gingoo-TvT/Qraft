#include "generator.hpp"
#include <cassert>
#include <climits>
#include <queue>
#include <set>
using namespace std;

void check_tree(int n, const vector<af::Edge>& edges, int degree=0, bool binary=false) {
    assert(int(edges.size())==n-1);
    vector<vector<int>> adj(n+1);
    set<pair<int,int>> unique;
    for(auto [u,v]:edges) {
        assert(u>=1 && u<=n && v>=1 && v<=n && u!=v);
        assert(unique.insert(minmax(u,v)).second);
        adj[u].push_back(v); adj[v].push_back(u);
    }
    vector<int> parent(n+1,-1); queue<int> q; q.push(1); parent[1]=0;
    int visited=0;
    while(!q.empty()) {
        int u=q.front(); q.pop(); ++visited; int children=0;
        for(int v:adj[u]) if(v!=parent[u]) {
            assert(parent[v]==-1); parent[v]=u; q.push(v); ++children;
        }
        if(binary) assert(children<=2);
        if(degree) assert(int(adj[u].size())<=degree);
    }
    assert(visited==n);
}
int main() {
    af::Random golden(0);
    assert(golden.next()==0xe220a8397b1dcdafULL);
    for(int seed=0;seed<100;++seed) {
        af::Random rng(seed), repeat(seed);
        for(int j=0;j<30;++j) assert(rng.integer(LLONG_MIN,LLONG_MAX)==repeat.integer(LLONG_MIN,LLONG_MAX));
        auto wide=rng.distinct(1000,LLONG_MIN,LLONG_MAX);
        assert(set<long long>(wide.begin(),wide.end()).size()==wide.size());
        auto full=rng.distinct(13,-6,6); sort(full.begin(),full.end());
        for(int i=0;i<13;++i) assert(full[i]==i-6);
        auto perm=rng.permutation(100); sort(perm.begin(),perm.end());
        for(int i=0;i<100;++i) assert(perm[i]==i+1);
        auto text=rng.palindrome(31,"ab");
        assert(text==string(text.rbegin(),text.rend()));
        for(string shape:{"random","chain","star","binary"}) {
            auto edges=rng.tree(100,shape);
            check_tree(100,edges,0,shape=="binary");
        }
        check_tree(100,rng.degree_tree(100,2),2);
        check_tree(100,rng.degree_tree(100,3),3);
        for(int n:{1,2,3,12,31}) {
            int total=n*(n-1)/2;
            for(int m:{0,total/2,total}) {
                auto edges=rng.graph(n,m);
                assert(int(edges.size())==m);
                set<pair<int,int>> unique;
                for(auto [u,v]:edges) assert(u>=1 && u<v && v<=n && unique.insert({u,v}).second);
                auto dag=rng.dag(n,m);
                vector<vector<int>> adj(n+1); vector<int> degree(n+1);
                for(auto [u,v]:dag) {adj[u].push_back(v); ++degree[v];}
                queue<int> q; for(int u=1;u<=n;++u) if(!degree[u]) q.push(u);
                int count=0;
                while(!q.empty()) {int u=q.front();q.pop();++count;for(int v:adj[u]) if(!--degree[v]) q.push(v);}
                assert(count==n);
            }
            for(int m:{n-1,total}) {
                auto edges=rng.graph(n,m,true);
                assert(int(edges.size())==m);
                vector<int> parent(n+1); iota(parent.begin(),parent.end(),0);
                auto root=[&](int x){while(parent[x]!=x) x=parent[x];return x;};
                set<pair<int,int>> unique;
                for(auto [u,v]:edges) {assert(u>=1 && u<v && v<=n && unique.insert({u,v}).second);parent[root(u)]=root(v);}
                for(int u=1;u<=n;++u) assert(root(u)==root(1));
            }
        }
    }
    af::Random rng(5);
    check_tree(4,rng.tree(4,"binary"),0,true);
    check_tree(200000,rng.degree_tree(200000,3),3);
    check_tree(200000,rng.tree(200000,"binary"),0,true);
    auto rejects=[](auto f){bool caught=false;try{f();}catch(const invalid_argument&){caught=true;}assert(caught);};
    rejects([&]{rng.array(-1,0,1);});
    rejects([&]{rng.integer(5,4);});
    rejects([&]{rng.distinct(4,0,2);});
    rejects([&]{rng.graph(2,2);});
    rejects([&]{rng.graph(4,2,true);});
    rejects([&]{rng.degree_tree(4,1);});
    rejects([&]{rng.tree(0);});
    rejects([&]{rng.tree(4,"typo");});
    rejects([&]{rng.text(1,"");});
    cout<<"properties passed: 100 seeds, integer extremes, dense graphs, rooted binary trees, 200000-node stress\n";
}
