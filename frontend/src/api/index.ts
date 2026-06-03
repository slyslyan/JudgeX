import axios from 'axios'

const api = axios.create({
  baseURL: '/api',
  timeout: 30000,
})

api.interceptors.request.use((config) => {
  const token = localStorage.getItem('token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

api.interceptors.response.use(
  (res) => res,
  (err) => {
    if (err.response?.status === 401) {
      localStorage.removeItem('token')
      localStorage.removeItem('user')
      window.location.href = '/login'
    }
    return Promise.reject(err)
  }
)

export interface RankEntry {
  user_id: number
  username: string
  solved: number
}

export interface User {
  id: number
  username: string
  role: string
}

export interface ProblemTag {
  id: number
  name: string
}

export interface Problem {
  id: number
  number: number
  title: string
  description: string
  time_limit: number
  memory_limit: number
  sample_cases: { input: string; output: string }[] | null
  tags: ProblemTag[]
  accepted_count: number
  submission_count: number
  created_at: string
}

export interface Submission {
  id: number
  user_id: number
  problem_id: number
  problem_title: string
  language: string
  code: string
  status: string
  time_used: number
  memory_used: number
  passed_count: number
  total_cases: number
  error_message: string
  created_at: string
}

export interface RunResult {
  status: string
  stdout: string
  stderr: string
  time_used: number
  memory_used: number
}

export interface Profile {
  id: number
  username: string
  email: string
  role: string
  created_at: string
  total_submissions: number
  accepted_submissions: number
  solved_problems: number
  recent_submissions: Submission[]
}

export interface TestCase {
  id: number
  problem_id: number
  input: string
  expected: string
  is_example: boolean
}

// Auth
export const login = (username: string, password: string) =>
  api.post<{ token: string; user_id: number; username: string; role: string }>('/auth/login', { username, password })

export const register = (username: string, email: string, password: string, confirmPassword: string) =>
  api.post<{ token: string; user_id: number; username: string; role: string }>('/auth/register', { username, email, password, confirm_password: confirmPassword })

// Problems
export const getProblems = (page = 1, pageSize = 20, search = '', tag = '') =>
  api.get<{ problems: Problem[]; total: number; tags: ProblemTag[] }>('/problems', { params: { page, page_size: pageSize, search, tag } })

export const getProblem = (id: number) =>
  api.get<Problem>(`/problems/${id}`)

export const createProblem = (title: string, description: string, timeLimit: number, memoryLimit: number, sampleCases?: { input: string; output: string }[], tags?: string[], number?: number) =>
  api.post<Problem>('/problems', { title, description, time_limit: timeLimit, memory_limit: memoryLimit, sample_cases: sampleCases, tags, number })

export const updateProblem = (id: number, data: Partial<Problem> & { tags?: string[] }) =>
  api.put<Problem>(`/problems/${id}`, data)

export const deleteProblem = (id: number) =>
  api.delete(`/problems/${id}`)

// Submissions
export const submitCode = (problemId: number, language: string, code: string) =>
  api.post<{ submission_id: number; status: string; cached?: boolean }>('/submissions', { problem_id: problemId, language, code })

export const getSubmissions = (cursor = 0, pageSize = 20, filters?: { status?: string; problem_id?: number; language?: string; mine?: boolean }) =>
  api.get<{ submissions: Submission[]; next_cursor: number; has_more: boolean; page_size: number }>('/submissions', { params: { cursor, page_size: pageSize, ...filters } })

export const getSubmission = (id: number) =>
  api.get<Submission>(`/submissions/${id}`)

export const rejudgeSubmission = (id: number) =>
  api.post<{ message: string }>(`/submissions/${id}/rejudge`)

export const runCode = (language: string, code: string, input: string, timeLimit?: number, memoryLimit?: number) =>
  api.post<RunResult>('/run', { language, code, input, time_limit: timeLimit, memory_limit: memoryLimit })

// Contests
export const getContests = (page = 1, pageSize = 20) =>
  api.get<{ contests: any[]; total: number }>('/contests', { params: { page, page_size: pageSize } })

export const getContest = (id: number) =>
  api.get<{ contest: any; problems: any[] }>(`/contests/${id}`)

export const createContest = (data: {
  title: string
  description: string
  start_time: string
  end_time: string
  rule_type: string
}) => api.post('/contests', data)

export const updateContest = (id: number, data: {
  title: string
  description: string
  start_time: string
  end_time: string
  rule_type: string
}) => api.put(`/contests/${id}`, data)

export const addContestProblem = (contestId: number, problemId: number, displayId: string) =>
  api.post(`/contests/${contestId}/problems`, { problem_id: problemId, display_id: displayId })

export const removeContestProblem = (contestId: number, problemId: number) =>
  api.delete(`/contests/${contestId}/problems/${problemId}`)

export const submitContestCode = (contestId: number, problemId: number, language: string, code: string) =>
  api.post<{ submission_id: number; status: string }>(`/contests/${contestId}/submissions`, { problem_id: problemId, language, code })

export const getContestLeaderboard = (contestId: number) =>
  api.get<{ leaderboard: { rank: number; user_id: number; username: string; solved: number; penalty: number }[] }>(`/contests/${contestId}/leaderboard`)

// Profile
export interface Profile {
  id: number
  username: string
  email: string
  bio: string
  role: string
  created_at: string
  total_submissions: number
  accepted_submissions: number
  solved_problems: number
  recent_submissions: Submission[]
}

export const getProfile = () =>
  api.get<Profile>('/profile')

export const getUserProfile = (userId: number) =>
  api.get<Profile>(`/users/${userId}`)

export const updateProfile = (data: { email?: string; bio?: string }) =>
  api.put<{ message: string }>('/profile', data)

export const changePassword = (oldPassword: string, newPassword: string) =>
  api.put<{ message: string; token: string }>('/profile/password', { old_password: oldPassword, new_password: newPassword })

export const getTemplates = () =>
  api.get<{ templates: Record<string, string> | null }>('/profile/templates')

export const saveTemplates = (templates: Record<string, string>) =>
  api.put<{ message: string }>('/profile/templates', { templates })

export const getLastCode = (problemId: number) =>
  api.get<{ code: string | null; language: string | null }>(`/problems/${problemId}/last-code`)

// Leaderboard
export const getLeaderboard = () =>
  api.get<{ leaderboard: RankEntry[] }>('/leaderboard')

// Admin
export const listUsers = () =>
  api.get<{ users: { id: number; username: string; email: string; role: string }[] }>('/admin/users')

export const updateUserRole = (userId: number, role: string) =>
  api.put(`/admin/users/${userId}/role`, { role })

export const deleteUser = (userId: number) =>
  api.delete(`/admin/users/${userId}`)

// Announcements
export interface Announcement {
  id: number
  title: string
  content: string
  created_at: string
  updated_at: string
}

export const getAnnouncements = () =>
  api.get<{ announcements: Announcement[] }>('/announcements')

export const createAnnouncement = (title: string, content: string) =>
  api.post<Announcement>('/admin/announcements', { title, content })

export const updateAnnouncement = (id: number, title: string, content: string) =>
  api.put<Announcement>(`/admin/announcements/${id}`, { title, content })

export const deleteAnnouncement = (id: number) =>
  api.delete(`/admin/announcements/${id}`)

// Test cases
export const addTestCase = (problemId: number, input: string, expected: string, isExample = false) =>
  api.post<TestCase>(`/problems/${problemId}/testcases`, { input, expected, is_example: isExample })

export const getTestCases = (problemId: number, type?: string) =>
  api.get<TestCase[]>(`/problems/${problemId}/testcases`, { params: type ? { type } : {} })

export const updateTestCase = (testCaseId: number, data: { input?: string; expected?: string; is_example?: boolean }) =>
  api.put<TestCase>(`/testcases/${testCaseId}`, data)

export const deleteTestCase = (testCaseId: number) =>
  api.delete(`/testcases/${testCaseId}`)

export const deleteAllTestCases = (problemId: number) =>
  api.delete(`/admin/problems/${problemId}/testcases`)

export interface SingleTestCase {
  case_id: number
  input: string
  expected: string
}

export const addSingleTestCase = (problemId: number, input: string, expected: string) =>
  api.post<{ message: string; case_id: number }>(`/admin/problems/${problemId}/testcases/single`, { input, expected })

export const getSingleTestCase = (problemId: number, caseId: number) =>
  api.get<SingleTestCase>(`/admin/problems/${problemId}/testcases/${caseId}`)

export const updateSingleTestCase = (problemId: number, caseId: number, input: string, expected: string) =>
  api.put<{ message: string; case_id: number }>(`/admin/problems/${problemId}/testcases/${caseId}`, { input, expected })

export const deleteSingleTestCase = (problemId: number, caseId: number) =>
  api.delete(`/admin/problems/${problemId}/testcases/${caseId}`)

export interface DiskCaseInfo {
  case_id: number
  input_size: number
  out_size: number
}

export const listDiskTestCases = (problemId: number) =>
  api.get<{ problem_id: number; test_case_version: number; cases: DiskCaseInfo[]; count: number }>(`/admin/problems/${problemId}/testcases/disk`)
