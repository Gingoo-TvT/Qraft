export default function ProviderFailureNotice({ failure }: { failure: string }) {
  const modelService = /\b(?:openai(?:-responses|-chat)?|anthropic|gemini|llm)\b/i.test(failure);
  const authentication = /\b401\b|\bunauthorized\b|invalid[_ ](?:api[_ ])?(?:key|token)|authentication_error/i.test(failure);
  if (!modelService || !authentication) return null;

  const role = /GenerateStatementActivity|statement generation/i.test(failure)
    ? '生成（G）模型'
    : '对应阶段的模型';
  return (
    <div className="mt-3 border-t border-danger-400/20 pt-3 text-sm text-danger-600 dark:text-danger-400">
      <p className="font-medium">模型服务鉴权未通过</p>
      <p className="mt-1">
        请管理员核对{role}的服务地址、API Key 和访问权限，并检查本次是否启用了模型覆盖。
        该错误不足以判断是密钥失效、权限限制还是本次覆盖配置有误。
      </p>
      <p className="mt-2">
        此任务使用创建时的模型配置；原任务“重试”仍沿用该配置。
        修改配置后请重新创建任务，使用更新后的设置。
      </p>
    </div>
  );
}
