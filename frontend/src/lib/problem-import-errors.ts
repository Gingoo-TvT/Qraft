export function importFailureSummary(error: string): string {
 if (error.includes('ImportModelTimeout') || error.includes('StartToClose timeout')) return '模型等待超时，这道题暂未完成；其他题目会继续处理。';
 if (error.includes('InvalidImportModelResponse')) return '模型返回格式不完整，两次修复后仍无法使用。可稍后单独继续。';
 if (error.includes('AssessmentFailed') || error.includes('structured response')) return '模型返回的结构不符合要求。已经保存的题目和数据会保留。';
 if (error.includes('test-case count') || error.includes('test_cases')) return '测试数据结果未通过结构或数量检查，尚未作为有效数据入库。';
 if (error.includes('parse solution') || error.includes('parsing main solution') || error.includes('parsing brute solution')) return '参考解法的返回格式不正确，未能进入验证阶段。';
 if (error.includes('401') || error.includes('Invalid token')) return '模型服务认证失败，请管理员检查当前模型配置。';
 return '这道题未完成，请查看错误详情；其他题目不受影响。';
}
